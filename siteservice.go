package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed icon.png
var iconPNG []byte

//go:embed autofill-extension/manifest.json
var extManifest []byte

//go:embed autofill-extension/content.js
var extContentJS []byte

//go:embed autofill-extension/credentials.js
var extCredentialsJS []byte

type Site struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Icon      string    `json:"icon"`
	Category  string    `json:"category"`
	OpenMode  string    `json:"openMode"`
	Username  string    `json:"username"`
	Password  string    `json:"password"` // encrypted, AES-GCM with machine key
	AutoLogin bool      `json:"autoLogin"`
	SortOrder int       `json:"sortOrder"`
	CreatedAt time.Time `json:"createdAt"`
}

type SiteService struct {
	mu               sync.RWMutex
	sites            []Site
	chrome           string
	hasChrome        bool
	pendingMu        sync.Mutex
	pendingOpen      string
	listener         net.Listener
	winMu            sync.Mutex
	openWindows      map[string]uintptr
	pendingWindows   map[string]bool
	autoOpen         string
	mainWindowHidden bool
	settings         map[string]string
}

func NewSiteService() *SiteService {
	s := &SiteService{
		sites:          make([]Site, 0),
		openWindows:    make(map[string]uintptr),
		pendingWindows: make(map[string]bool),
		settings:       make(map[string]string),
	}
	s.chrome, s.hasChrome = findChrome()
	iconPath := s.ensureIcon()
	s.installDesktopEntry(iconPath)
	s.installAutofillExtension()
	s.startIPCListener()
	s.loadSettings()
	s.load()
	if len(s.sites) == 0 {
		s.sites = defaultSites()
		s.save()
	}
	return s
}

func (s *SiteService) SetAutoOpen(url string) {
	s.autoOpen = normalizeURL(url)
}

func (s *SiteService) StartAutoOpen() {
	if s.autoOpen == "" {
		return
	}
	url := s.autoOpen
	go func() {
		time.Sleep(800 * time.Millisecond)
		s.OpenSite(url)
	}()
}

func (s *SiteService) IsChromeModeSite(url string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	url = normalizeURL(url)
	for _, site := range s.sites {
		if site.URL == url && site.OpenMode == "chrome" {
			return true
		}
	}
	return false
}

func (s *SiteService) HasChrome() bool {
	return s.hasChrome
}

func (s *SiteService) ChromePath() string {
	return s.chrome
}

// GetCredentials returns decrypted username and password for a site by URL.
// Used by the auto-fill extension.
func (s *SiteService) GetCredentials(url string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	url = normalizeURL(url)
	for _, site := range s.sites {
		if matchURL(site.URL, url) && site.Username != "" {
			password, _ := decrypt(site.Password)
			return map[string]string{
				"username":  site.Username,
				"password":  password,
				"autoLogin": fmt.Sprintf("%v", site.AutoLogin),
			}
		}
	}
	return nil
}

// WriteCredentialsForExtension writes all site credentials into credentials.js
// for the Chrome extension content script to read directly.
func (s *SiteService) WriteCredentialsForExtension() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type credEntry struct {
		URL       string `json:"url"`
		Username  string `json:"username"`
		Password  string `json:"password"`
		AutoLogin bool   `json:"autoLogin"`
	}

	var entries []credEntry
	for _, site := range s.sites {
		if site.Username == "" || site.Password == "" {
			continue
		}
		password, _ := decrypt(site.Password)
		entries = append(entries, credEntry{
			URL:       site.URL,
			Username:  site.Username,
			Password:  password,
			AutoLogin: site.AutoLogin,
		})
	}

	data, err := json.MarshalIndent(entries, "  ", "  ")
	if err != nil {
		return err
	}

	// Write as a JS file that sets window.__WEBDESK_CREDENTIALS__
	jsContent := "// WebDesk AutoFill - Credentials (auto-generated)\n" +
		"window.__WEBDESK_CREDENTIALS__ = " + string(data) + ";\n"

	dir := s.extensionDir()
	os.MkdirAll(dir, 0755)
	return os.WriteFile(filepath.Join(dir, "credentials.js"), []byte(jsContent), 0600)
}

// extensionDir returns the path where the Chrome extension files are stored.
func (s *SiteService) extensionDir() string {
	cacheDir, _ := os.UserCacheDir()
	return filepath.Join(cacheDir, "webdesk", "autofill-extension")
}

func (s *SiteService) GetAll() []Site {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Site, len(s.sites))
	copy(result, s.sites)
	return result
}

func (s *SiteService) GetByCategory(category string) []Site {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Site
	for _, site := range s.sites {
		if site.Category == category {
			result = append(result, site)
		}
	}
	return result
}

func (s *SiteService) GetCategories() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]bool)
	var result []string
	for _, site := range s.sites {
		if !seen[site.Category] {
			seen[site.Category] = true
			result = append(result, site.Category)
		}
	}
	return result
}

func (s *SiteService) Create(name, url, icon, category, openMode, username, password string, autoLogin bool) Site {
	s.mu.Lock()
	defer s.mu.Unlock()
	if openMode == "" {
		openMode = "embed"
	}
	// Encrypt password before storing
	encPassword, err := encrypt(password)
	if err != nil {
		encPassword = ""
	}
	site := Site{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		Name:      name,
		URL:       normalizeURL(url),
		Icon:      icon,
		Category:  category,
		OpenMode:  openMode,
		Username:  username,
		Password:  encPassword,
		AutoLogin: autoLogin,
		SortOrder: len(s.sites),
		CreatedAt: time.Now(),
	}
	s.sites = append(s.sites, site)
	s.save()
	return site
}

func (s *SiteService) Update(id, name, url, icon, category, openMode, username, password string, autoLogin bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if openMode == "" {
		openMode = "embed"
	}
	for i := range s.sites {
		if s.sites[i].ID == id {
			s.sites[i].Name = name
			s.sites[i].URL = normalizeURL(url)
			s.sites[i].Icon = icon
			s.sites[i].Category = category
			s.sites[i].OpenMode = openMode
			s.sites[i].Username = username
			// Only re-encrypt if password changed (non-empty means user typed a new one)
			if password != "" {
				encPassword, err := encrypt(password)
				if err != nil {
					encPassword = ""
				}
				s.sites[i].Password = encPassword
			}
			s.sites[i].AutoLogin = autoLogin
			s.save()
			return nil
		}
	}
	return fmt.Errorf("site not found: %s", id)
}

func (s *SiteService) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.sites {
		if s.sites[i].ID == id {
			s.sites = append(s.sites[:i], s.sites[i+1:]...)
			s.save()
			return nil
		}
	}
	return fmt.Errorf("site not found: %s", id)
}

func (s *SiteService) Reorder(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	siteMap := make(map[string]Site)
	for _, site := range s.sites {
		siteMap[site.ID] = site
	}
	var reordered []Site
	for i, id := range ids {
		if site, ok := siteMap[id]; ok {
			site.SortOrder = i
			reordered = append(reordered, site)
		}
	}
	s.sites = reordered
	s.save()
	return nil
}

func (s *SiteService) SetMainWindowHidden(hidden bool) {
	s.mainWindowHidden = hidden
}

func (s *SiteService) WindowFocus() {
	app := application.Get()
	w := app.Window.Current()
	if w != nil {
		if s.mainWindowHidden {
			w.Show()
			s.mainWindowHidden = false
		}
		w.Focus()
	}
}

func (s *SiteService) SetTitle(title string) {
	app := application.Get()
	w := app.Window.Current()
	if w != nil {
		w.SetTitle(title)
	}
}

func (s *SiteService) SetOpacity(opacity float64) {
	// opacity: 0.0 (invisible) ~ 1.0 (fully opaque)
	if opacity < 0.1 {
		opacity = 0.1
	}
	if opacity > 1.0 {
		opacity = 1.0
	}
	// Set main window opacity
	setLinuxWindowOpacity(opacity)
	// Set all Chrome app windows opacity
	value := uint32(opacity * 0xFFFFFFFF)
	s.winMu.Lock()
	for _, wid := range s.openWindows {
		setWindowOpacity(wid, value)
	}
	s.winMu.Unlock()
}

func (s *SiteService) OpenSite(url string) error {
	if !s.hasChrome {
		return fmt.Errorf("Chrome not found")
	}

	s.winMu.Lock()
	if wid, ok := s.openWindows[url]; ok {
		s.winMu.Unlock()
		if activateX11Window(wid) {
			return nil
		}
		s.winMu.Lock()
		delete(s.openWindows, url)
		s.winMu.Unlock()
	} else {
		s.winMu.Unlock()
	}

	s.winMu.Lock()
	if s.pendingWindows[url] {
		s.winMu.Unlock()
		return nil
	}
	s.pendingWindows[url] = true
	s.winMu.Unlock()

	existingWindows := findAllChromeAppWindows()

	cacheDir, _ := os.UserCacheDir()
	userDataDir := filepath.Join(cacheDir, "webdesk", "chrome-profile")
	os.MkdirAll(userDataDir, 0755)

	// Kill any stale Chrome processes using our user-data-dir that don't have debug port
	s.killStaleChromeProcesses(userDataDir)

	// Only remove lock files if Chrome is NOT running.
	// If Chrome is running, removing SingletonLock will break the running instance's
	// ability to detect that another process is trying to open a URL via --app=.
	chromeAlreadyRunning := cdpCheckPort(9222)
	if !chromeAlreadyRunning {
		s.removeChromeLock(userDataDir)
	}

	const debugPort = 9222

	// Always launch Chrome via exec.Command with --app mode.
	// When Chrome is already running with the same --user-data-dir, it will
	// forward the --app=URL request to the existing instance, which creates
	// a new app window (not a browser tab). This ensures consistent --app behavior.
	args := []string{
		"--app=" + url,
		"--user-data-dir=" + userDataDir,
		"--remote-debugging-port=" + strconv.Itoa(debugPort),
		"--ignore-certificate-errors",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-sync",
		"--disable-features=TranslateUI",
		"--disable-extensions-except=" + s.extensionDir(),
		"--load-extension=" + s.extensionDir(),
	}

	fmt.Println("[WebDesk] OpenSite: launching Chrome with args:", strings.Join(args, " "))
	cmd := exec.Command(s.chrome, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
	setChromeProcessAttr(cmd)
	if err := cmd.Start(); err != nil {
		fmt.Println("[WebDesk] OpenSite: Chrome launch failed:", err)
		s.winMu.Lock()
		delete(s.pendingWindows, url)
		s.winMu.Unlock()
		return err
	}
	fmt.Println("[WebDesk] OpenSite: Chrome process started, PID:", cmd.Process.Pid)
	go s.adoptChromeWindow(url, existingWindows)

	// Inject auto-fill via CDP
	go s.injectAutoFillViaCDP(url, debugPort)

	return nil
}
// findFreePort asks the OS for a free TCP port.
func findFreePort() int {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		return 9222 // fallback
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 9222
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}


// injectAutoFillViaCDP connects to Chrome's DevTools Protocol and injects
// the auto-fill JavaScript directly into the page.
func (s *SiteService) injectAutoFillViaCDP(url string, requestedPort int) {
	s.mu.RLock()
	var site *Site
	normalizedURL := normalizeURL(url)
	for i := range s.sites {
		if matchURL(s.sites[i].URL, normalizedURL) && s.sites[i].Username != "" {
			site = &s.sites[i]
			break
		}
	}
	s.mu.RUnlock()

	if site == nil {
		fmt.Println("[WebDesk] CDP: no credentials for", url)
		return
	}

	password, err := decrypt(site.Password)
	if err != nil || password == "" {
		fmt.Println("[WebDesk] CDP: decrypt failed")
		return
	}

	fmt.Println("[WebDesk] CDP: injecting auto-fill for", url, "username:", site.Username)

	// Strategy: find the actual debug port Chrome is using.
	debugPort := s.findDebugPort(requestedPort)
	if debugPort == 0 {
		fmt.Println("[WebDesk] CDP: could not find any debug port, giving up")
		return
	}
	fmt.Println("[WebDesk] CDP: using debug port:", debugPort)

	script := generateFillScript(site.Username, password, site.AutoLogin)

	// Step 1: Register script on the browser level via browser WebSocket.
	// This uses Page.addScriptToEvaluateOnNewDocument so that ANY new page
	// that loads will automatically run our fill script.
	// This handles the case where a new --app window is created by Chrome
	// and cdpFindTarget hasn't found it yet.
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", debugPort))
	if err != nil {
		fmt.Println("[WebDesk] CDP: /json/version failed:", err)
	} else {
		var versionInfo map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&versionInfo)
		resp.Body.Close()
		wsURL, _ := versionInfo["webSocketDebuggerUrl"].(string)
		if wsURL != "" {
			fmt.Println("[WebDesk] CDP: registering script on browser wsURL:", wsURL)
			bws, err := wsDial(wsURL)
			if err == nil {
				// Register on browser-level: this applies to ALL new pages
				cdpCall(bws, 1, "Page.enable", nil)
				cdpCall(bws, 2, "Page.addScriptToEvaluateOnNewDocument", map[string]interface{}{
					"source": script,
				})
				fmt.Println("[WebDesk] CDP: browser-level script registered OK")
				bws.close()
			}
		}
	}

	// Step 2: Also find the specific target page and inject directly.
	// This handles the case where the page is already loaded.
	// Wait for the target page to appear
	target, err := cdpFindTarget(debugPort, url, 20*time.Second)
	if err != nil {
		fmt.Println("[WebDesk] CDP: target not found:", err)
		return
	}
	fmt.Println("[WebDesk] CDP: target found:", target.URL)

	ws, err := wsDial(target.WSURL)
	if err != nil {
		fmt.Println("[WebDesk] CDP: ws connect failed:", err)
		return
	}
	defer ws.close()

	// Enable Page domain and register script on this specific target too
	cdpCall(ws, 1, "Page.enable", nil)
	cdpCall(ws, 2, "Page.addScriptToEvaluateOnNewDocument", map[string]interface{}{
		"source": script,
	})

	// Also try Runtime.evaluate as a fallback - in case the page is already loaded
	cdpCall(ws, 3, "Runtime.enable", nil)

	// Wait a moment for the page to potentially finish loading before evaluate
	time.Sleep(2 * time.Second)

	_, err = cdpCall(ws, 4, "Runtime.evaluate", map[string]interface{}{
		"expression": script,
	})
	if err != nil {
		fmt.Println("[WebDesk] CDP: evaluate failed:", err)
		return
	}
	fmt.Println("[WebDesk] CDP: script injected successfully, response:", resp)
}

// openTabViaCDP opens a new tab in an already-running Chrome instance via CDP.
// NOTE: Currently not used by OpenSite (which always uses exec.Command for --app mode).
// Kept as fallback. Always clears pendingWindows when done.
func (s *SiteService) openTabViaCDP(url string, port int) {
	defer func() {
		s.winMu.Lock()
		delete(s.pendingWindows, url)
		s.winMu.Unlock()
	}()

	fmt.Println("[WebDesk] CDP: opening new tab for", url)

	// Find the browser WebSocket URL from /json/version
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
	if err != nil {
		fmt.Println("[WebDesk] CDP: /json/version failed:", err)
		return
	}
	defer resp.Body.Close()
	var versionInfo map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&versionInfo)
	wsURL, _ := versionInfo["webSocketDebuggerUrl"].(string)
	if wsURL == "" {
		fmt.Println("[WebDesk] CDP: no browser wsURL found")
		return
	}
	fmt.Println("[WebDesk] CDP: browser wsURL:", wsURL)

	ws, err := wsDial(wsURL)
	if err != nil {
		fmt.Println("[WebDesk] CDP: ws connect to browser failed:", err)
		return
	}
	defer ws.close()

	// Create a new tab
	result, err := cdpCall(ws, 1, "Target.createTarget", map[string]interface{}{
		"url": url,
	})
	if err != nil {
		fmt.Println("[WebDesk] CDP: createTarget failed:", err)
		return
	}
	fmt.Println("[WebDesk] CDP: new tab created, response:", result)
}

// findDebugPort tries multiple strategies to find Chrome's debug port.
// removeChromeLock removes the SingletonLock file that prevents Chrome
// from starting when a previous instance did not shut down cleanly.
// killStaleChromeProcesses kills any Chrome processes that are using our user-data-dir
// but do NOT have a debug port listening. This prevents stale processes from
// blocking new Chrome launches on Windows.
func (s *SiteService) killStaleChromeProcesses(userDataDir string) {
	if cdpCheckPort(9222) {
		return // Debug port active, Chrome is running properly
	}

	// Debug port not active but Chrome may have a stale process
	fmt.Println("[WebDesk] checking for stale Chrome processes...")

	switch runtime.GOOS {
	case "windows":
		// On Windows, find chrome.exe processes with our user-data-dir and kill them
		// Use wmic to find processes by command line
		out, err := exec.Command("wmic", "process", "where",
			"commandline like '%webdesk\\chrome-profile%' and name='chrome.exe'",
			"get", "processid", "/value").Output()
		if err != nil {
			fmt.Println("[WebDesk] wmic query failed:", err)
			return
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "ProcessId=") {
				pid := strings.TrimPrefix(line, "ProcessId=")
				pid = strings.TrimSpace(pid)
				if pid != "" {
					fmt.Println("[WebDesk] killing stale Chrome PID:", pid)
					exec.Command("taskkill", "/F", "/PID", pid).Run()
				}
			}
		}
	default:
		// Linux: pkill chrome processes with our profile
		exec.Command("pkill", "-f", "webdesk/chrome-profile").Run()
	}

	// Wait for processes to die
	time.Sleep(500 * time.Millisecond)
}

func (s *SiteService) removeChromeLock(userDataDir string) {
	lockFile := filepath.Join(userDataDir, "SingletonLock")
	if _, err := os.Stat(lockFile); err == nil {
		fmt.Println("[WebDesk] removing stale Chrome SingletonLock")
		os.Remove(lockFile)
	}
	// Also remove SingletonCookie and SingletonSocket for good measure
	os.Remove(filepath.Join(userDataDir, "SingletonCookie"))
	os.Remove(filepath.Join(userDataDir, "SingletonSocket"))
}

func (s *SiteService) findDebugPort(requestedPort int) int {
	// Strategy 1: Try the port we requested (works on Linux, usually on Windows too if Chrome is fresh)
	fmt.Println("[WebDesk] CDP: trying requested port:", requestedPort)
	if cdpCheckPort(requestedPort) {
		fmt.Println("[WebDesk] CDP: requested port is active")
		return requestedPort
	}

	// Strategy 2: Read DevToolsActivePort from user data dir (Windows fallback)
	cacheDir, _ := os.UserCacheDir()
	userDataDir := filepath.Join(cacheDir, "webdesk", "chrome-profile")
	portFile := filepath.Join(userDataDir, "DevToolsActivePort")
	fmt.Println("[WebDesk] CDP: trying DevToolsActivePort at:", portFile)

	// Wait a bit for the file to appear
	for i := 0; i < 20; i++ {
		data, err := os.ReadFile(portFile)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) > 0 {
				port, err := strconv.Atoi(strings.TrimSpace(lines[0]))
				if err == nil && port > 0 {
					fmt.Println("[WebDesk] CDP: found port from DevToolsActivePort:", port)
					if cdpCheckPort(port) {
						return port
					}
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Strategy 3: Scan common debug ports (9222-9240)
	fmt.Println("[WebDesk] CDP: scanning ports 9222-9240")
	for port := 9222; port <= 9240; port++ {
		if cdpCheckPort(port) {
			fmt.Println("[WebDesk] CDP: found active debug port:", port)
			return port
		}
	}

	return 0
}

// cdpCheckPort checks if a CDP endpoint is responding on the given port.
func cdpCheckPort(port int) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}
func (s *SiteService) adoptChromeWindow(url string, existing map[uintptr]bool) {
	defer func() {
		s.winMu.Lock()
		delete(s.pendingWindows, url)
		s.winMu.Unlock()
	}()

	for i := 0; i < 50; i++ {
		time.Sleep(200 * time.Millisecond)
		current := findAllChromeAppWindows()
		s.winMu.Lock()
		for wid := range current {
			if !existing[wid] {
				alreadyTracked := false
				for _, tracked := range s.openWindows {
					if tracked == wid {
						alreadyTracked = true
						break
					}
				}
				if !alreadyTracked {
					setWMClass(wid, "webdesk", "Webdesk")
					s.openWindows[url] = wid
					s.winMu.Unlock()
					return
				}
			}
		}
		s.winMu.Unlock()
	}
}

func (s *SiteService) cleanStaleWindows() {
	for url, wid := range s.openWindows {
		if !isWindowValid(wid) {
			delete(s.openWindows, url)
		}
	}
}

func (s *SiteService) IsSiteOpen(url string) bool {
	s.winMu.Lock()
	defer s.winMu.Unlock()
	s.cleanStaleWindows()
	_, ok := s.openWindows[url]
	if !ok {
		ok = s.pendingWindows[url]
	}
	return ok
}

func (s *SiteService) CreateShortcut(id string) error {
	s.mu.RLock()
	var site *Site
	for i := range s.sites {
		if s.sites[i].ID == id {
			site = &s.sites[i]
			break
		}
	}
	s.mu.RUnlock()
	if site == nil {
		return fmt.Errorf("site not found: %s", id)
	}

	home, _ := os.UserHomeDir()
	safeName := sanitizeFilename(site.Name)

	switch runtime.GOOS {
	case "windows":
		desktopDir := filepath.Join(home, "Desktop")
		os.MkdirAll(desktopDir, 0755)
		exe, _ := os.Executable()
		shortcutPath := filepath.Join(desktopDir, safeName+".lnk")
		psScript := fmt.Sprintf(
			`$ws = New-Object -ComObject WScript.Shell; $sc = $ws.CreateShortcut('%s'); $sc.TargetPath = '%s'; $sc.Arguments = '--open=%s'; $sc.IconLocation = '%s,0'; $sc.Save()`,
			shortcutPath, exe, site.URL, exe,
		)
		cmd := exec.Command("powershell", "-NoProfile", "-Command", psScript)
		cmd.Stdout = nil
		cmd.Stderr = nil
		fmt.Printf("[WebDesk] CreateShortcut: path=%s exe=%s args=--open=%s\n", shortcutPath, exe, site.URL)
		err := cmd.Run()
		if err != nil {
			fmt.Printf("[WebDesk] CreateShortcut error: %v\n", err)
		}
		return err

	default:
		desktopDir := filepath.Join(home, "Desktop")
		os.MkdirAll(desktopDir, 0755)
		shortcutPath := filepath.Join(desktopDir, safeName+".desktop")
		exe, _ := os.Executable()
		execLine := exe + " --open=" + site.URL
		content := "[Desktop Entry]\n" +
			"Type=Application\n" +
			"Name=" + site.Name + "\n" +
			"Comment=Open " + site.Name + "\n" +
			"Exec=" + execLine + "\n" +
			"Icon=" + s.ensureIcon() + "\n" +
			"Terminal=false\n" +
			"Categories=WebBrowser;\n" +
			"StartupNotify=true\n" +
			"StartupWMClass=webdesk\n"
		if err := os.WriteFile(shortcutPath, []byte(content), 0644); err != nil {
			return err
		}
		return os.Chmod(shortcutPath, 0755)
	}
}

func sanitizeFilename(name string) string {
	replacer := map[rune]bool{
		'/': true, '\\': true, ':': true, '*': true,
		'?': true, '"': true, '<': true, '>': true, '|': true,
	}
	result := make([]rune, 0, len(name))
	for _, r := range name {
		if replacer[r] {
			result = append(result, '_')
		} else {
			result = append(result, r)
		}
	}
	s := string(result)
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}

func (s *SiteService) OpenInBrowser(url string) {
	if s.hasChrome {
		s.OpenSite(url)
	}
}

func findChrome() (string, bool) {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		candidates = []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LocalAppData"), "Google", "Chrome", "Application", "chrome.exe"),
		}
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}
	default:
		candidates = []string{
			"/usr/bin/google-chrome-stable",
			"/usr/bin/google-chrome",
			"/usr/bin/chromium-browser",
			"/usr/bin/chromium",
			"/snap/bin/chromium",
		}
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
	}
	if p, err := exec.LookPath("google-chrome-stable"); err == nil {
		return p, true
	}
	if p, err := exec.LookPath("google-chrome"); err == nil {
		return p, true
	}
	if p, err := exec.LookPath("chromium-browser"); err == nil {
		return p, true
	}
	if p, err := exec.LookPath("chromium"); err == nil {
		return p, true
	}
	return "", false
}

func (s *SiteService) startIPCListener() {
	cacheDir, _ := os.UserCacheDir()
	dir := filepath.Join(cacheDir, "webdesk")
	os.MkdirAll(dir, 0755)
	sockPath := filepath.Join(dir, "ipc.sock")
	os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return
	}
	s.listener = ln

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 4096)
			n, _ := conn.Read(buf)
			url := strings.TrimSpace(string(buf[:n]))
			conn.Close()
			if url == "" {
				continue
			}
			s.mu.RLock()
			var site *Site
			for i := range s.sites {
				if s.sites[i].URL == url {
					site = &s.sites[i]
					break
				}
			}
			s.mu.RUnlock()

			if site != nil && site.OpenMode == "chrome" && s.hasChrome {
				s.OpenSite(url)
			} else {
				s.pendingMu.Lock()
				s.pendingOpen = url
				s.pendingMu.Unlock()
				go func() {
					time.Sleep(100 * time.Millisecond)
					s.WindowFocus()
				}()
			}
		}
	}()
}

func (s *SiteService) PopPendingOpen() string {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	url := s.pendingOpen
	s.pendingOpen = ""
	return url
}

func SendToExistingInstance(url string) bool {
	cacheDir, _ := os.UserCacheDir()
	sockPath := filepath.Join(cacheDir, "webdesk", "ipc.sock")

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	conn.Write([]byte(url))
	return true
}

func (s *SiteService) IconPath() string {
	return s.ensureIcon()
}

func (s *SiteService) ensureIcon() string {
	cacheDir, _ := os.UserCacheDir()
	dir := filepath.Join(cacheDir, "webdesk")
	os.MkdirAll(dir, 0755)
	p := filepath.Join(dir, "webdesk.png")
	os.WriteFile(p, iconPNG, 0644)
	return p
}

// installAutofillExtension writes the Chrome extension files to the cache directory.
func (s *SiteService) installAutofillExtension() {
	dir := s.extensionDir()
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "manifest.json"), extManifest, 0644)
	os.WriteFile(filepath.Join(dir, "content.js"), extContentJS, 0644)
	// Write initial credentials.js (will be updated when sites are saved)
	s.WriteCredentialsForExtension()
}

func (s *SiteService) installDesktopEntry(iconPath string) {
	home, _ := os.UserHomeDir()
	appDir := filepath.Join(home, ".local", "share", "applications")
	os.MkdirAll(appDir, 0755)

	oldPath := filepath.Join(appDir, "webdesk.desktop")
	newPath := filepath.Join(appDir, "org.wails.webdesk.desktop")

	os.Remove(oldPath)

	exe, _ := os.Executable()

	content := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=WebDesk\n" +
		"Comment=把网站当应用用的桌面壳\n" +
		"Exec=" + exe + "\n" +
		"Icon=" + iconPath + "\n" +
		"Terminal=false\n" +
		"Categories=Network;WebBrowser;Utility;\n" +
		"Keywords=web;browser;launcher;\n" +
		"StartupNotify=true\n" +
		"StartupWMClass=webdesk\n"

	existing, err := os.ReadFile(newPath)
	if err == nil && string(existing) == content {
		return
	}
	os.WriteFile(newPath, []byte(content), 0644)
	os.Chmod(newPath, 0755)

	s.updateFavorites()
}

func (s *SiteService) updateFavorites() {
	out, err := exec.Command("gsettings", "get", "org.gnome.shell", "favorite-apps").Output()
	if err != nil {
		return
	}
	favs := string(out)
	target := "org.wails.webdesk.desktop"
	if strings.Contains(favs, target) {
		if !strings.Contains(favs, "'webdesk.desktop'") {
			return
		}
	}
	newFavs := strings.ReplaceAll(favs, "'webdesk.desktop'", "'"+target+"'")
	if !strings.Contains(newFavs, target) {
		newFavs = strings.TrimRight(strings.TrimSpace(newFavs), "]") + ", '" + target + "]"
	}
	newFavs = strings.TrimSpace(newFavs)
	exec.Command("gsettings", "set", "org.gnome.shell", "favorite-apps", newFavs).Run()
}

func (s *SiteService) configPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	dir := filepath.Join(configDir, "webdesk")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "sites.json")
}

func (s *SiteService) load() {
	path := s.configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	json.Unmarshal(data, &s.sites)
}

func (s *SiteService) save() {
	data, err := json.MarshalIndent(s.sites, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(s.configPath(), data, 0644)
}

func (s *SiteService) SaveSettings(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[key] = value
	s.saveSettings()
}

func (s *SiteService) LoadSetting(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings[key]
}

func (s *SiteService) settingsPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	dir := filepath.Join(configDir, "webdesk")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "settings.json")
}

func (s *SiteService) loadSettings() {
	path := s.settingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	json.Unmarshal(data, &s.settings)
}

func (s *SiteService) saveSettings() {
	data, err := json.MarshalIndent(s.settings, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(s.settingsPath(), data, 0644)
}

func normalizeURL(url string) string {
	if url == "" {
		return ""
	}
	if len(url) > 7 && url[:7] == "http://" {
		return url
	}
	if len(url) > 8 && url[:8] == "https://" {
		return url
	}
	return "https://" + url
}

func defaultSites() []Site {
	now := time.Now()
	return []Site{
		{ID: "1", Name: "邮箱", URL: "https://mail.company.com", Icon: "📧", Category: "常用", OpenMode: "embed", SortOrder: 0, CreatedAt: now},
		{ID: "2", Name: "BI报表", URL: "https://bi.company.com", Icon: "📊", Category: "常用", OpenMode: "chrome", SortOrder: 1, CreatedAt: now},
		{ID: "3", Name: "Wiki", URL: "https://wiki.company.com", Icon: "📝", Category: "常用", OpenMode: "chrome", SortOrder: 2, CreatedAt: now},
		{ID: "4", Name: "GitLab", URL: "https://git.company.com", Icon: "🐙", Category: "开发", OpenMode: "embed", SortOrder: 3, CreatedAt: now},
		{ID: "5", Name: "Jira", URL: "https://jira.company.com", Icon: "📋", Category: "开发", OpenMode: "chrome", SortOrder: 4, CreatedAt: now},
		{ID: "6", Name: "Jenkins", URL: "https://ci.company.com", Icon: "🔧", Category: "开发", OpenMode: "embed", SortOrder: 5, CreatedAt: now},
	}
}
