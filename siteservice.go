package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed icon.png
var iconPNG []byte

type Site struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Icon      string    `json:"icon"`
	Category  string    `json:"category"`
	OpenMode  string    `json:"openMode"`
	SortOrder int       `json:"sortOrder"`
	CreatedAt time.Time `json:"createdAt"`
}

type SiteService struct {
	mu             sync.RWMutex
	sites          []Site
	chrome         string
	hasChrome      bool
	pendingMu      sync.Mutex
	pendingOpen    string
	listener       net.Listener
	winMu          sync.Mutex
	openWindows    map[string]uintptr
	pendingWindows map[string]bool
}

func NewSiteService() *SiteService {
	s := &SiteService{
		sites:          make([]Site, 0),
		openWindows:    make(map[string]uintptr),
		pendingWindows: make(map[string]bool),
	}
	s.chrome, s.hasChrome = findChrome()
	iconPath := s.ensureIcon()
	s.installDesktopEntry(iconPath)
	s.startIPCListener()
	s.load()
	if len(s.sites) == 0 {
		s.sites = defaultSites()
		s.save()
	}
	return s
}

func (s *SiteService) HasChrome() bool {
	return s.hasChrome
}

func (s *SiteService) ChromePath() string {
	return s.chrome
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

func (s *SiteService) Create(name, url, icon, category, openMode string) Site {
	s.mu.Lock()
	defer s.mu.Unlock()
	if openMode == "" {
		openMode = "embed"
	}
	site := Site{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		Name:      name,
		URL:       normalizeURL(url),
		Icon:      icon,
		Category:  category,
		OpenMode:  openMode,
		SortOrder: len(s.sites),
		CreatedAt: time.Now(),
	}
	s.sites = append(s.sites, site)
	s.save()
	return site
}

func (s *SiteService) Update(id, name, url, icon, category, openMode string) error {
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

func (s *SiteService) WindowFocus() {
	app := application.Get()
	w := app.Window.Current()
	if w != nil {
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

	cmd := exec.Command(s.chrome, "--app="+url)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := cmd.Start(); err != nil {
		s.winMu.Lock()
		delete(s.pendingWindows, url)
		s.winMu.Unlock()
		return err
	}

	go s.adoptChromeWindow(url, existingWindows)

	return nil
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
		shortcutPath := filepath.Join(desktopDir, safeName+".url")
		content := "[InternetShortcut]\r\n" +
			"URL=" + site.URL + "\r\n" +
			"IconIndex=0\r\n"
		if s.hasChrome && site.OpenMode == "chrome" {
			content += "IconFile=" + s.chrome + "\r\n"
		}
		return os.WriteFile(shortcutPath, []byte(content), 0644)

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
	if _, err := os.Stat(p); err == nil {
		return p
	}
	os.WriteFile(p, iconPNG, 0644)
	return p
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
