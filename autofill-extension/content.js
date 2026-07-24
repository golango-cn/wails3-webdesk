/**
 * WebDesk AutoFill Extension - Content Script (with debug logging)
 */

(function () {
  'use strict';

  const TAG = '[WebDesk-AutoFill]';

  function log(...args) { console.log(TAG, ...args); }
  function warn(...args) { console.warn(TAG, ...args); }

  const credentials = window.__WEBDESK_CREDENTIALS__ || [];
  log('Loaded, credentials count:', credentials.length);
  if (credentials.length > 0) {
    log('Credentials URLs:', credentials.map(c => c.url));
  }
  log('Current page URL:', window.location.href);

  if (credentials.length === 0) {
    warn('No credentials found, exiting');
    return;
  }

  function matchURL(siteURL, pageURL) {
    const s = siteURL.replace(/\/+$/, '');
    const p = pageURL.replace(/\/+$/, '');
    if (s === p) return true;
    if (p.startsWith(s + '/') || p.startsWith(s + '?') || p.startsWith(s + '#')) return true;
    // Also match if page URL starts with site URL (handles SPA routes)
    try {
      const siteU = new URL(s);
      const pageU = new URL(p);
      if (siteU.origin === pageU.origin && siteU.pathname === pageU.pathname) return true;
      if (pageU.href.startsWith(siteU.origin + siteU.pathname)) return true;
    } catch (e) {}
    return false;
  }

  function findPasswordField() {
    let pw = document.querySelector('input[type="password"]');
    if (pw) { log('Found password field via type="password"'); return pw; }
    const allInputs = document.querySelectorAll('input');
    for (const inp of allInputs) {
      if (inp.autocomplete === 'current-password' || inp.autocomplete === 'new-password') {
        log('Found password field via autocomplete');
        return inp;
      }
    }
    return null;
  }

  function findUsernameField(passwordField) {
    // Strategy 1: Find text input immediately before password in DOM order
    // Most login forms have username right before password
    const allInputs = document.querySelectorAll('input');
    let pwIdx = -1;
    for (let i = 0; i < allInputs.length; i++) {
      if (allInputs[i] === passwordField) { pwIdx = i; break; }
    }
    if (pwIdx > 0) {
      for (let i = pwIdx - 1; i >= 0; i--) {
        const inp = allInputs[i];
        const t = (inp.type || '').toLowerCase();
        if (t === 'text' || t === 'email' || t === 'tel' || t === '' || t === 'number') {
          log('Found username via DOM-order (before password), name:', inp.name, 'id:', inp.id);
          return inp;
        }
      }
    }

    // Strategy 2: autocomplete attribute
    const form = passwordField.closest('form');
    const searchScope = form || document;
    log('Searching username in', form ? 'form' : 'document');

    const candidates = searchScope.querySelectorAll('input:not([type="hidden"]):not([type="password"]):not([type="checkbox"]):not([type="radio"]):not([type="submit"]):not([type="button"]):not([type="file"]):not([type="image"])');
    log('Username candidates count:', candidates.length);

    for (const inp of candidates) {
      if (inp.autocomplete === 'username' || inp.autocomplete === 'email') {
        log('Found username via autocomplete:', inp.autocomplete);
        return inp;
      }
    }

    // Strategy 3: name/id/placeholder keyword matching
    for (const inp of candidates) {
      const name = (inp.name + ' ' + inp.id + ' ' + inp.placeholder + ' ' + (inp.getAttribute('data-vv-name') || '') + ' ' + (inp.getAttribute('aria-label') || '')).toLowerCase();
      if (/user|name|email|account|login|acct|手机|账号|账户/.test(name)) {
        log('Found username via name/id/placeholder:', name.trim());
        return inp;
      }
    }

    // Strategy 4: first visible text input in scope
    for (const inp of candidates) {
      if (inp.type === 'text' || inp.type === 'email' || inp.type === 'tel' || inp.type === '') {
        if (inp.offsetParent !== null) {
          log('Found username via fallback (visible text input), type:', inp.type, 'name:', inp.name);
          return inp;
        }
      }
    }

    warn('No username field found');
    return null;
  }

  function setInputValue(input, value) {
    log('Setting value on element:', input.tagName, 'name:', input.name, 'id:', input.id);
    input.focus();
    const nativeSetter = Object.getOwnPropertyDescriptor(
      window.HTMLInputElement.prototype, 'value'
    )?.set;
    if (nativeSetter) {
      nativeSetter.call(input, value);
    } else {
      input.value = value;
    }
    input.dispatchEvent(new Event('focus', { bubbles: true }));
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
    input.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }));
    input.dispatchEvent(new KeyboardEvent('keyup', { key: ' ', bubbles: true }));
    input.dispatchEvent(new Event('blur', { bubbles: true }));
    log('Value set, current input value:', input.value);
  }

  function checkAllCheckboxes(form) {
    if (!form) return;
    const checkboxes = form.querySelectorAll('input[type="checkbox"]');
    if (checkboxes.length > 0) {
      log('Found', checkboxes.length, 'checkboxes, checking all');
    }
    checkboxes.forEach(cb => {
      if (!cb.checked) cb.click();
    });
  }

  function submitForm(form) {
    if (!form) { warn('No form to submit'); return false; }

    const submitBtn = form.querySelector('button[type="submit"], input[type="submit"]');
    if (submitBtn && !submitBtn.disabled) {
      log('Clicking submit button');
      submitBtn.click();
      return true;
    }

    const buttons = form.querySelectorAll('button, a[role="button"], span[role="button"], div[role="button"]');
    for (const btn of buttons) {
      const text = (btn.textContent + ' ' + btn.value + ' ' + (btn.getAttribute('aria-label') || '')).toLowerCase();
      if (/login|sign\s*in|登[录陸]|确[认定]|submit|next/.test(text) && !btn.disabled) {
        log('Clicking button by text:', text.trim());
        btn.click();
        return true;
      }
    }

    try { log('Trying form.submit()'); form.submit(); return true; } catch (e) { warn('form.submit() failed:', e); return false; }
  }

  let fillAttempted = false;

  function autofill() {
    const pageURL = window.location.href;
    log('autofill() called, page:', pageURL);

    const cred = credentials.find(c => {
      const matched = matchURL(c.url, pageURL);
      log('  Comparing:', c.url, 'vs', pageURL, '→', matched);
      return matched;
    });

    if (!cred) {
      log('No matching credential for this page');
      return;
    }

    log('Matched credential for:', cred.url, 'username:', cred.username, 'autoLogin:', cred.autoLogin);

    const passwordField = findPasswordField();
    if (!passwordField) {
      log('Password field not found yet (form not rendered)');
      return;
    }

    if (passwordField.value && passwordField.value.length > 0) {
      log('Password field already has value, skipping');
      return;
    }

    const usernameField = findUsernameField(passwordField);
    const form = passwordField.closest('form');
    log('Form found:', !!form);

    if (usernameField && cred.username) {
      setInputValue(usernameField, cred.username);
    }
    if (cred.password) {
      setInputValue(passwordField, '********');
    }

    checkAllCheckboxes(form);

    if (cred.autoLogin && !fillAttempted) {
      fillAttempted = true;
      log('Auto-login enabled, submitting in 500ms');
      setTimeout(() => submitForm(form), 500);
    }
  }

  // Initial attempt after 2s (let framework bootstrap)
  log('Scheduling first attempt in 2s');
  setTimeout(autofill, 2000);

  // Poll every 1.5s for up to 20s
  let pollCount = 0;
  const pollInterval = setInterval(() => {
    pollCount++;
    log('Poll attempt', pollCount);
    autofill();
    if (pollCount >= 13 || fillAttempted) {
      clearInterval(pollInterval);
      log('Polling stopped, fillAttempted:', fillAttempted);
    }
  }, 1500);

  // MutationObserver
  let observerAttempts = 0;
  const observer = new MutationObserver(() => {
    if (observerAttempts < 20) {
      observerAttempts++;
      if (observerAttempts % 5 === 0) log('MutationObserver triggered, attempt', observerAttempts);
      setTimeout(autofill, 500);
    } else {
      observer.disconnect();
    }
  });

  setTimeout(() => {
    if (document.body) {
      observer.observe(document.body, { childList: true, subtree: true });
      log('MutationObserver started');
    }
  }, 1000);

  log('Content script initialized');
})();
