package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cdpTarget represents a Chrome DevTools Protocol target.
type cdpTarget struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	WSURL string `json:"webSocketDebuggerUrl"`
}

// waitForDebugPort reads the DevToolsActivePort file from the Chrome user data dir.
func waitForDebugPort(userDataDir string, timeout time.Duration) (int, error) {
	portFile := filepath.Join(userDataDir, "DevToolsActivePort")
	fmt.Println("[WebDesk] CDP: waiting for DevToolsActivePort at:", portFile)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(portFile)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			fmt.Println("[WebDesk] CDP: DevToolsActivePort content lines:", len(lines))
			if len(lines) > 0 {
				port, err := strconv.Atoi(strings.TrimSpace(lines[0]))
				if err == nil && port > 0 {
					fmt.Println("[WebDesk] CDP: parsed debug port:", port)
					return port, nil
				}
				fmt.Println("[WebDesk] CDP: failed to parse port from:", strings.TrimSpace(lines[0]), "err:", err)
			}
		} else {
			fmt.Println("[WebDesk] CDP: DevToolsActivePort not found yet:", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return 0, fmt.Errorf("timeout waiting for DevToolsActivePort in %s", portFile)
}

// cdpFindTarget polls the CDP HTTP endpoint to find a target matching the URL prefix.
func cdpFindTarget(port int, urlPrefix string, timeout time.Duration) (*cdpTarget, error) {
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/json/list", port)
	fmt.Println("[WebDesk] CDP: polling endpoint:", endpoint, "for URL prefix:", urlPrefix)

	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(endpoint)
		if err != nil {
			fmt.Println("[WebDesk] CDP: GET /json/list failed:", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var targets []cdpTarget
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		err = json.Unmarshal(body, &targets)
		if err != nil {
			fmt.Println("[WebDesk] CDP: JSON parse failed:", err, "body:", string(body)[:min(200, len(body))])
			time.Sleep(1000 * time.Millisecond)
			continue
		}
			fmt.Println("[WebDesk] CDP: found", len(targets), "targets")
			// Find the LAST matching target (newest page) instead of the first
			var lastMatch *cdpTarget
			for i := range targets {
				fmt.Println("[WebDesk] CDP:   target", i, "url:", targets[i].URL, "wsURL:", targets[i].WSURL)
				if strings.HasPrefix(targets[i].URL, urlPrefix) && targets[i].WSURL != "" {
					lastMatch = &targets[i]
				}
				if strings.HasPrefix(targets[i].URL+"/", urlPrefix) && targets[i].WSURL != "" {
					lastMatch = &targets[i]
				}
			}
			if lastMatch != nil {
				return lastMatch, nil
			}
			time.Sleep(500 * time.Millisecond)
	}
	return nil, fmt.Errorf("timeout finding CDP target for %s", urlPrefix)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- Minimal WebSocket client ---

type wsConn struct {
	conn net.Conn
}

func wsDial(urlStr string) (*wsConn, error) {
	fmt.Println("[WebDesk] CDP: wsDial connecting to:", urlStr)

	rest := strings.TrimPrefix(urlStr, "ws://")
	slashIdx := strings.Index(rest, "/")
	var host, path string
	if slashIdx >= 0 {
		host = rest[:slashIdx]
		path = rest[slashIdx:]
	} else {
		host = rest
		path = "/"
	}

	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("ws dial: %w", err)
	}

	keyBytes := make([]byte, 16)
	for i := range keyBytes {
		keyBytes[i] = byte(i*17%256) + 1
	}
	wsKey := encodeBase64(keyBytes)

	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, host, wsKey)
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ws write upgrade: %w", err)
	}

	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ws read upgrade: %w", err)
	}
	resp := string(buf[:n])
	if !strings.Contains(resp, "101") {
		conn.Close()
		return nil, fmt.Errorf("ws upgrade failed: %s", strings.Split(resp, "\r\n")[0])
	}

	fmt.Println("[WebDesk] CDP: WebSocket connected successfully")
	return &wsConn{conn: conn}, nil
}

func (w *wsConn) sendText(msg string) error {
	payload := []byte(msg)
	maskKey := [4]byte{0x37, 0xa5, 0x12, 0xf0}

	frame := []byte{0x81}
	length := len(payload)
	if length <= 125 {
		frame = append(frame, byte(0x80|length))
	} else if length <= 65535 {
		frame = append(frame, 0x80|126)
		frame = append(frame, byte(length>>8), byte(length))
	} else {
		frame = append(frame, 0x80|127)
		for i := 7; i >= 0; i-- {
			frame = append(frame, byte(length>>(i*8)))
		}
	}
	frame = append(frame, maskKey[:]...)
	masked := make([]byte, length)
	for i, b := range payload {
		masked[i] = b ^ maskKey[i%4]
	}
	frame = append(frame, masked...)
	_, err := w.conn.Write(frame)
	return err
}

func (w *wsConn) recvText() (string, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(w.conn, header); err != nil {
		return "", err
	}
	opcode := header[0] & 0x0f
	if opcode == 0x8 {
		return "", fmt.Errorf("websocket close")
	}
	if opcode == 0x9 {
		length := header[1] & 0x7f
		payload := make([]byte, length)
		io.ReadFull(w.conn, payload)
		pong := []byte{0x8A, 0x00}
		w.conn.Write(pong)
		return w.recvText()
	}
	length := uint64(header[1] & 0x7f)
	if length == 126 {
		ext := make([]byte, 2)
		if _, err := io.ReadFull(w.conn, ext); err != nil {
			return "", err
		}
		length = uint64(ext[0])<<8 | uint64(ext[1])
	} else if length == 127 {
		ext := make([]byte, 8)
		if _, err := io.ReadFull(w.conn, ext); err != nil {
			return "", err
		}
		length = binary.BigEndian.Uint64(ext)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(w.conn, payload); err != nil {
		return "", err
	}
	return string(payload), nil
}

func (w *wsConn) close() {
	w.conn.Close()
}

func encodeBase64(data []byte) string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	result := make([]byte, 0, (len(data)+2)/3*4)
	for i := 0; i < len(data); i += 3 {
		var n uint32
		remaining := len(data) - i
		if remaining >= 3 {
			n = uint32(data[i])<<16 | uint32(data[i+1])<<8 | uint32(data[i+2])
			result = append(result, charset[n>>18&0x3f], charset[n>>12&0x3f], charset[n>>6&0x3f], charset[n&0x3f])
		} else if remaining == 2 {
			n = uint32(data[i])<<16 | uint32(data[i+1])<<8
			result = append(result, charset[n>>18&0x3f], charset[n>>12&0x3f], charset[n>>6&0x3f], '=')
		} else {
			n = uint32(data[i]) << 16
			result = append(result, charset[n>>18&0x3f], charset[n>>12&0x3f], '=', '=')
		}
	}
	return string(result)
}

func cdpCall(ws *wsConn, id int, method string, params map[string]interface{}) (map[string]interface{}, error) {
	msg := map[string]interface{}{
		"id":     id,
		"method": method,
	}
	if params != nil {
		msg["params"] = params
	}
	data, _ := json.Marshal(msg)
	fmt.Println("[WebDesk] CDP: sending command:", method, "id:", id)
	if err := ws.sendText(string(data)); err != nil {
		return nil, fmt.Errorf("cdp send: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		respStr, err := ws.recvText()
		if err != nil {
			return nil, fmt.Errorf("cdp recv: %w", err)
		}
		var resp map[string]interface{}
		if err := json.Unmarshal([]byte(respStr), &resp); err != nil {
			continue
		}
		if respID, ok := resp["id"]; ok {
			idFloat, ok := respID.(float64)
			if ok && int(idFloat) == id {
				fmt.Println("[WebDesk] CDP: received response for id:", id)
				return resp, nil
			}
		}
	}
	return nil, fmt.Errorf("cdp timeout waiting for response id=%d", id)
}


// generateFillScript creates JavaScript that fills login forms for the given URL.
func generateFillScript(username, password string, autoLogin bool) string {
	autoLoginJS := "false"
	if autoLogin {
		autoLoginJS = "true"
	}
	js := `(function() {
		var TAG = '[WebDesk-AutoFill]';
		var cred = {username: ` + jsonStr(username) + `, password: ` + jsonStr(password) + `, autoLogin: ` + autoLoginJS + `};
		console.log(TAG, '=== CDP inject START === filling form for', window.location.href);
		console.log(TAG, 'cred: username=' + cred.username + ', autoLogin=' + cred.autoLogin);

		function findPasswordField() {
			console.log(TAG, '>>> findPasswordField: searching...');
			var all = document.querySelectorAll('input');
			console.log(TAG, '>>> findPasswordField: total inputs on page:', all.length);
			for (var i = 0; i < all.length; i++) {
				console.log(TAG, '>>>   input[' + i + ']: type=' + all[i].type + ' name=' + all[i].name + ' id=' + all[i].id + ' autocomplete=' + all[i].autocomplete);
			}
			var pw = document.querySelector('input[type="password"]');
			if (pw) { console.log(TAG, '>>> findPasswordField: FOUND via type="password", name=' + pw.name + ' id=' + pw.id); return pw; }
			for (var i = 0; i < all.length; i++) {
				if (all[i].autocomplete === 'current-password' || all[i].autocomplete === 'new-password') {
					console.log(TAG, '>>> findPasswordField: FOUND via autocomplete, name=' + all[i].name + ' id=' + all[i].id);
					return all[i];
				}
			}
			console.log(TAG, '>>> findPasswordField: NOT FOUND');
			return null;
		}

		function isEditableInput(inp) {
			if (!inp) return false;
			if (inp.disabled) { console.log(TAG, '>>> isEditableInput: SKIP disabled, name=' + inp.name + ' id=' + inp.id); return false; }
			if (inp.readOnly) { console.log(TAG, '>>> isEditableInput: SKIP readOnly, name=' + inp.name + ' id=' + inp.id); return false; }
			var t = (inp.type || '').toLowerCase();
			if (t === 'hidden' || t === 'password' || t === 'checkbox' || t === 'radio' || t === 'submit' || t === 'button' || t === 'file' || t === 'image') {
				console.log(TAG, '>>> isEditableInput: SKIP type=' + t + ', name=' + inp.name + ' id=' + inp.id);
				return false;
			}
			var rect = inp.getBoundingClientRect();
			if (rect.width <= 0 && rect.height <= 0 && inp.offsetParent === null) {
				console.log(TAG, '>>> isEditableInput: SKIP not visible, name=' + inp.name + ' id=' + inp.id);
				return false;
			}
			console.log(TAG, '>>> isEditableInput: OK, name=' + inp.name + ' id=' + inp.id + ' type=' + inp.type);
			return true;
		}

		function findUsernameField(pw) {
			console.log(TAG, '>>> findUsernameField: searching...');
			// Strategy 1: Find text input immediately before password in DOM order
			var allInputs = document.querySelectorAll('input');
			var pwIdx = -1;
			for (var i = 0; i < allInputs.length; i++) {
				if (allInputs[i] === pw) { pwIdx = i; break; }
			}
			console.log(TAG, '>>> findUsernameField: password at DOM index ' + pwIdx + ', total inputs: ' + allInputs.length);
			if (pwIdx > 0) {
				console.log(TAG, '>>> Strategy 1: scanning backwards from pwIdx=' + pwIdx);
				for (var i = pwIdx - 1; i >= 0; i--) {
					var inp = allInputs[i];
					console.log(TAG, '>>>   checking input[' + i + ']: type=' + inp.type + ' name=' + inp.name + ' id=' + inp.id + ' disabled=' + inp.disabled + ' readOnly=' + inp.readOnly);
					if (isEditableInput(inp)) {
						console.log(TAG, '>>> Strategy 1 HIT: name=' + inp.name + ' id=' + inp.id + ' type=' + inp.type);
						return inp;
					}
				}
				console.log(TAG, '>>> Strategy 1: no editable input found before password');
			}

			// Strategy 2: autocomplete attribute
			var form = pw.closest('form');
			var scope = form || document;
			console.log(TAG, '>>> Strategy 2: autocomplete search in ' + (form ? 'form' : 'document'));
			var cands = scope.querySelectorAll('input:not([type="hidden"]):not([type="password"]):not([type="checkbox"]):not([type="radio"]):not([type="submit"]):not([type="button"]):not([type="file"])');
			console.log(TAG, '>>> Strategy 2: candidates count: ' + cands.length);
			for (var i = 0; i < cands.length; i++) {
				if (cands[i].autocomplete === 'username' || cands[i].autocomplete === 'email') {
					console.log(TAG, '>>> Strategy 2 HIT: autocomplete=' + cands[i].autocomplete + ' name=' + cands[i].name + ' id=' + cands[i].id);
					return cands[i];
				}
			}

			// Strategy 3: name/id/placeholder keyword matching
			console.log(TAG, '>>> Strategy 3: keyword matching');
			for (var i = 0; i < cands.length; i++) {
				var n = (cands[i].name + ' ' + cands[i].id + ' ' + cands[i].placeholder + ' ' + (cands[i].getAttribute('aria-label')||'')).toLowerCase();
				if (/user|name|email|account|login|手机|账号|账户/.test(n)) {
					console.log(TAG, '>>> Strategy 3 HIT: matched keywords in "' + n + '"');
					return cands[i];
				}
			}

			// Strategy 4: first visible text input in scope
			console.log(TAG, '>>> Strategy 4: first visible text input');
			for (var i = 0; i < cands.length; i++) {
				if ((cands[i].type === 'text' || cands[i].type === 'email' || cands[i].type === 'tel' || cands[i].type === '') && cands[i].offsetParent !== null) {
					console.log(TAG, '>>> Strategy 4 HIT: name=' + cands[i].name + ' id=' + cands[i].id);
					return cands[i];
				}
			}
			console.log(TAG, '>>> findUsernameField: ALL STRATEGIES FAILED');
			return null;
		}

		function setVal(el, val) {
			console.log(TAG, '>>> setVal: START tag=' + el.tagName + ' name=' + el.name + ' id=' + el.id + ' type=' + el.type);
			console.log(TAG, '>>> setVal: before set el.value="' + el.value + '"');

			// Step 1: Focus
			el.focus();
			el.dispatchEvent(new Event('focus', {bubbles: true}));
			el.dispatchEvent(new FocusEvent('focusin', {bubbles: true}));

			// Step 2: Clear existing value first
			var nativeSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
			if (nativeSetter && nativeSetter.set) {
				nativeSetter.set.call(el, '');
			} else {
				el.value = '';
			}
			el.dispatchEvent(new Event('input', {bubbles: true}));
			el.dispatchEvent(new Event('change', {bubbles: true}));

			// Step 3: Set value using native setter
			if (nativeSetter && nativeSetter.set) {
				nativeSetter.set.call(el, val);
				console.log(TAG, '>>> setVal: native setter applied');
			} else {
				el.value = val;
				console.log(TAG, '>>> setVal: direct value assignment');
			}

			// Step 4: Dispatch comprehensive events for Vue/React/Angular compatibility
			// InputEvent with insertText is the most compatible for Vue 3
			try {
				el.dispatchEvent(new InputEvent('input', {
					bubbles: true,
					cancelable: true,
					inputType: 'insertText',
					data: val
				}));
			} catch(e) {
				el.dispatchEvent(new Event('input', {bubbles: true}));
			}

			// Also dispatch a generic input event for older frameworks
			el.dispatchEvent(new Event('input', {bubbles: true}));
			el.dispatchEvent(new Event('change', {bubbles: true}));

			// Simulate keydown/keyup for the value
			el.dispatchEvent(new KeyboardEvent('keydown', {bubbles: true, key: ' ', keyCode: 32}));
			el.dispatchEvent(new KeyboardEvent('keyup', {bubbles: true, key: ' ', keyCode: 32}));

			// Blur to trigger validation
			el.dispatchEvent(new FocusEvent('focusout', {bubbles: true}));
			el.dispatchEvent(new Event('blur', {bubbles: true}));

			console.log(TAG, '>>> setVal: DONE - el.value="' + el.value + '" match=' + (el.value === val));
		}

		function checkCheckboxes(form) {
			if (!form) return;
			var cbs = form.querySelectorAll('input[type="checkbox"]');
			console.log(TAG, '>>> checkCheckboxes: found ' + cbs.length + ' checkboxes');
			for (var i = 0; i < cbs.length; i++) {
				if (!cbs[i].checked) {
					console.log(TAG, '>>> checkCheckboxes: clicking checkbox:', cbs[i].name || cbs[i].id || 'unnamed');
					cbs[i].click();
				}
			}
		}

		function forceClick(btn) {
			console.log(TAG, '>>> forceClick: button id=' + btn.id + ' class=' + btn.className + ' disabled=' + btn.disabled);
			// Remove disabled state - some frameworks keep button disabled until they detect input changes
			if (btn.disabled) {
				console.log(TAG, '>>> forceClick: removing disabled attribute...');
				btn.disabled = false;
				btn.removeAttribute('disabled');
			}
			// Remove disabled styles
			btn.style.cursor = 'pointer';
			btn.style.backgroundColor = '';
			console.log(TAG, '>>> forceClick: clicking button now, disabled=' + btn.disabled);
			btn.click();
			return true;
		}

		function clickLoginButton() {
			console.log(TAG, '>>> clickLoginButton: searching...');

			// Strategy 1: Search by known login button IDs
			var knownIds = ['logButton', 'loginBtn', 'login-btn', 'btnLogin', 'btn-login', 'submitBtn', 'submit-btn'];
			for (var i = 0; i < knownIds.length; i++) {
				var btn = document.getElementById(knownIds[i]);
				if (btn) {
					console.log(TAG, '>>> clickLoginButton: FOUND by id=' + knownIds[i] + ' disabled=' + btn.disabled);
					return forceClick(btn);
				}
			}

			// Strategy 2: submit type buttons
			var submitBtns = document.querySelectorAll('button[type="submit"], input[type="submit"]');
			console.log(TAG, '>>> clickLoginButton: found ' + submitBtns.length + ' submit buttons');
			for (var i = 0; i < submitBtns.length; i++) {
				console.log(TAG, '>>>   submitBtn[' + i + ']: disabled=' + submitBtns[i].disabled + ' text=' + (submitBtns[i].textContent||'').trim().substring(0,30));
				if (!submitBtns[i].disabled) {
					console.log(TAG, '>>> clickLoginButton: CLICKING submit button');
					submitBtns[i].click();
					return true;
				} else {
					console.log(TAG, '>>> clickLoginButton: submit button disabled, trying forceClick');
					return forceClick(submitBtns[i]);
				}
			}

			// Strategy 3: Search all buttons by text (handle spaces like "登  录")
			var allBtns = document.querySelectorAll('button, a[role="button"], span[role="button"], div[role="button"]');
			console.log(TAG, '>>> clickLoginButton: checking ' + allBtns.length + ' generic buttons');
			for (var i = 0; i < allBtns.length; i++) {
				var rawText = (allBtns[i].textContent || '').replace(/\s+/g, '').toLowerCase();
				var t = (allBtns[i].textContent + ' ' + allBtns[i].value + ' ' + (allBtns[i].getAttribute('aria-label')||'')).trim().toLowerCase();
				console.log(TAG, '>>>   btn[' + i + ']: id=' + allBtns[i].id + ' class=' + allBtns[i].className + ' disabled=' + allBtns[i].disabled + ' text="' + (allBtns[i].textContent||'').trim().substring(0,20) + '" rawText="' + rawText.substring(0,20) + '"');
				if (/login|signin|登[录陸]|确[认定]|submit|next/.test(rawText)) {
					console.log(TAG, '>>> clickLoginButton: FOUND button by text, trying forceClick');
					return forceClick(allBtns[i]);
				}
			}

			// Strategy 4: Find nearby button close to password field
			var pw = findPasswordField();
			if (pw) {
				var parent = pw.parentElement;
				for (var depth = 0; depth < 5 && parent; depth++) {
					var nearby = parent.querySelectorAll('button, [role="button"]');
					for (var i = 0; i < nearby.length; i++) {
						console.log(TAG, '>>> clickLoginButton: nearby button at depth=' + depth + ' id=' + nearby[i].id + ' disabled=' + nearby[i].disabled);
						return forceClick(nearby[i]);
					}
					parent = parent.parentElement;
				}
			}
			console.log(TAG, '>>> clickLoginButton: NO BUTTON FOUND');
			return false;
		}

		var done = false;
		function fill() {
			if (done) return;
			console.log(TAG, '>>> fill: attempt start...');
			var pw = findPasswordField();
			if (!pw) { console.log(TAG, '>>> fill: password field not found yet'); return; }
			if (pw.value && pw.value.length > 0) { console.log(TAG, '>>> fill: password already filled, len=' + pw.value.length); done = true; return; }

			var un = findUsernameField(pw);
			var form = pw.closest('form');
			console.log(TAG, '>>> fill: form=' + !!form + ', username=' + !!un);
			if (un) {
				console.log(TAG, '>>> fill: username detail: name=' + un.name + ' id=' + un.id + ' type=' + un.type + ' placeholder=' + un.placeholder + ' autocomplete=' + un.autocomplete + ' disabled=' + un.disabled);
			}
			console.log(TAG, '>>> fill: password detail: name=' + pw.name + ' id=' + pw.id + ' type=' + pw.type + ' autocomplete=' + pw.autocomplete);

			// Fill password first (more important), then username with error protection
			if (cred.password) {
				console.log(TAG, '>>> fill: about to fill PASSWORD...');
				try { setVal(pw, cred.password); } catch(e) { console.log(TAG, '>>> fill: PASSWORD fill ERROR:', e.message || e); }
			}
			if (un && cred.username) {
				console.log(TAG, '>>> fill: about to fill USERNAME...');
				try { setVal(un, cred.username); } catch(e) { console.log(TAG, '>>> fill: USERNAME fill ERROR:', e.message || e); }
			}
			checkCheckboxes(form);

			console.log(TAG, '>>> fill: DONE - username="' + (un ? un.value : 'N/A') + '" password len=' + (pw.value ? pw.value.length : 0));

			if (cred.autoLogin) {
				done = true;
				console.log(TAG, '>>> fill: username value="' + (un ? un.value : 'N/A') + '"');
				console.log(TAG, '>>> fill: password value="' + (pw.value ? pw.value.substring(0,2) + '***' : 'N/A') + '"');
				console.log(TAG, '>>> fill: password value length=' + (pw.value ? pw.value.length : 0));
				console.log(TAG, '>>> fill: auto-login scheduled in 2 seconds');
				setTimeout(function() { clickLoginButton(); }, 1000);
			}
		}

		console.log(TAG, '=== scheduling first fill in 800ms ===');
		setTimeout(fill, 800);
		var count = 0;
		var iv = setInterval(function() {
			count++;
			console.log(TAG, '>>> poll #' + count);
			fill();
			if (count >= 8 || done) clearInterval(iv);
		}, 1000);
		var obsCount = 0;
		var obs = new MutationObserver(function() {
			if (obsCount < 20) { obsCount++; setTimeout(fill, 500); } else obs.disconnect();
		});
		if (document.body) obs.observe(document.body, {childList: true, subtree: true});
	})();`
	return js
}

func jsonStr(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

// injectAutoFill (legacy, not called from OpenSite)
func (s *SiteService) injectAutoFill(url string) {
	s.mu.RLock()
	var site *Site
	normalizedURL := normalizeURL(url)
	for i := range s.sites {
		if s.sites[i].URL == normalizedURL && s.sites[i].Username != "" {
			site = &s.sites[i]
			break
		}
	}
	s.mu.RUnlock()

	if site == nil {
		fmt.Println("[WebDesk] injectAutoFill: no site with credentials for", url)
		return
	}

	password, err := decrypt(site.Password)
	if err != nil || password == "" {
		fmt.Println("[WebDesk] injectAutoFill: decrypt failed for", url)
		return
	}

	fmt.Println("[WebDesk] injectAutoFill: site found, username:", site.Username)

	cacheDir, _ := os.UserCacheDir()
	userDataDir := filepath.Join(cacheDir, "webdesk", "chrome-profile")

	port, err := waitForDebugPort(userDataDir, 10*time.Second)
	if err != nil {
		fmt.Println("[WebDesk] injectAutoFill: failed to get debug port:", err)
		return
	}
	fmt.Println("[WebDesk] injectAutoFill: debug port =", port)

	target, err := cdpFindTarget(port, url, 15*time.Second)
	if err != nil {
		fmt.Println("[WebDesk] injectAutoFill: failed to find target:", err)
		return
	}
	fmt.Println("[WebDesk] injectAutoFill: target found, wsURL:", target.WSURL)

	ws, err := wsDial(target.WSURL)
	if err != nil {
		fmt.Println("[WebDesk] injectAutoFill: ws connect failed:", err)
		return
	}
	defer ws.close()

	cdpCall(ws, 1, "Runtime.enable", nil)

	script := generateFillScript(site.Username, password, site.AutoLogin)
	resp, err := cdpCall(ws, 2, "Runtime.evaluate", map[string]interface{}{
		"expression": script,
		"awaitPromise": false,
	})
	if err != nil {
		fmt.Println("[WebDesk] injectAutoFill: evaluate failed:", err)
		return
	}
	fmt.Println("[WebDesk] injectAutoFill: script injected, response:", resp)
}
