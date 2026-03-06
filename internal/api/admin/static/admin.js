// admin.js — curl template dialog for the Accounts page.
//
// WHY a separate JS file (not inline in the templ template):
//   templ escapes content inside <script> tags by default, making inline JS fragile.
//   A static file served by the embedded FS is the canonical pattern for this project.
//   DEPENDENCY: admin.js must be added to the go:embed directive in static.go and
//   referenced by a <script src> tag in layout.templ.
//
// HOW the dialog works:
//   Each template button carries data-zone, data-zone-id, data-type, data-dynamic,
//   and data-method attrs. showCurlTemplate(btn) reads those attrs, builds the curl
//   command for the active method (POST / GET / DELETE), and opens the shared
//   <dialog id="curl-dialog"> (rendered once in AccountsPage).
//   The token input inside the dialog persists between openings because <dialog> stays
//   in the DOM — the user pastes their token once and all subsequent templates use it.
//   Three tabs (bash | cmd | PowerShell) switch the displayed command variant.

(function () {
  // recordName returns the record name placeholder for curl examples.
  // WHY {subdomain} in braces (not a literal word like "subdomain"):
  //   A literal "subdomain.example.com" looks like a real hostname — the operator
  //   might paste the example and forget to replace the prefix, accidentally targeting
  //   "subdomain.example.com" which likely doesn't exist or is someone else's record.
  //   Curly-brace syntax is universally recognised as "fill this in" and makes the
  //   placeholder impossible to mistake for a real hostname that should be kept as-is.
  function recordName(type, zone) {
    return '{subdomain}.' + zone;
  }

  // bodyForType returns the POST request body object for a given record type.
  function bodyForType(type, dynamic, zone) {
    var body;
    if (type === 'A') {
      body = { type: 'A', name: recordName(type, zone), content: '1.2.3.4', ttl: 300 };
      if (dynamic) {
        body.dynamic = true;
        body.ddns_key = 'optional-own-key';  // omit to auto-generate
      }
    } else if (type === 'AAAA') {
      body = { type: 'AAAA', name: recordName(type, zone), content: '2001:db8::1', ttl: 3600 };
    } else if (type === 'CNAME') {
      body = { type: 'CNAME', name: recordName(type, zone), content: 'target.example.com', ttl: 3600 };
    } else if (type === 'TXT') {
      body = { type: 'TXT', name: recordName(type, zone), content: 'v=spf1 ~all', ttl: 300 };
    }
    return body;
  }

  // --- POST builders ---

  // buildBash generates the bash/Linux curl POST command (multi-line, single-quoted JSON body).
  // WHY single-quoted JSON: no escaping needed inside single quotes in bash.
  function buildBash(base, zoneId, type, dynamic, zone, token) {
    var body = bodyForType(type, dynamic, zone);
    var bodyStr = JSON.stringify(body, null, 2);
    var cmd = (
      'curl -sk -X POST ' + base + '/api/v1/zones/' + zoneId + '/records \\\n' +
      '  -H "Authorization: Bearer ' + token + '" \\\n' +
      '  -H "Content-Type: application/json" \\\n' +
      "  -d '" + bodyStr + "'"
    );
    if (dynamic) {
      cmd += '\n\n# Response contains ddns_key \u2014 use it to push updates:\n' +
             '# curl "https://dyn.dns.he.net/nic/update' +
             '?hostname=subdomain.' + zone + '&password=YOUR_DDNS_KEY&myip=1.2.3.4"';
    }
    return cmd;
  }

  // buildCmd generates the Windows CMD curl POST command (single line, inner quotes as \").
  // WHY single line: CMD line continuation (^) is fragile; one line is more paste-friendly.
  // WHY \" escaping: CMD requires \" to embed literal double quotes inside a double-quoted string.
  function buildCmd(base, zoneId, type, dynamic, zone, token) {
    var body = bodyForType(type, dynamic, zone);
    var bodyStr = JSON.stringify(body).replace(/"/g, '\\"');
    var cmd = (
      'curl -sk -X POST ' + base + '/api/v1/zones/' + zoneId + '/records' +
      ' -H "Authorization: Bearer ' + token + '"' +
      ' -H "Content-Type: application/json"' +
      ' -d "' + bodyStr + '"'
    );
    if (dynamic) {
      cmd += '\r\n\r\n:: Response contains ddns_key \u2014 use it to push updates:\r\n' +
             ':: curl "https://dyn.dns.he.net/nic/update' +
             '?hostname=subdomain.' + zone + '&password=YOUR_DDNS_KEY&myip=1.2.3.4"';
    }
    return cmd;
  }

  // buildPs generates the PowerShell curl POST command (multi-line with backtick).
  // WHY curl.exe not curl: in PowerShell, `curl` is an alias for Invoke-WebRequest.
  // WHY single-quoted JSON: PowerShell expands $variables in double-quoted strings,
  //   breaking IP literals like "1.2.3.4" which contain no $ but could in other values.
  function buildPs(base, zoneId, type, dynamic, zone, token) {
    var body = bodyForType(type, dynamic, zone);
    var bodyStr = JSON.stringify(body);
    var cmd = (
      'curl.exe -sk -X POST ' + base + '/api/v1/zones/' + zoneId + '/records `\n' +
      '  -H "Authorization: Bearer ' + token + '" `\n' +
      '  -H "Content-Type: application/json" `\n' +
      "  -d '" + bodyStr + "'"
    );
    if (dynamic) {
      cmd += '\n\n# Response contains ddns_key \u2014 use it to push updates:\n' +
             '# curl.exe "https://dyn.dns.he.net/nic/update' +
             '?hostname=subdomain.' + zone + '&password=YOUR_DDNS_KEY&myip=1.2.3.4"';
    }
    return cmd;
  }

  // --- GET builders ---
  // GET /api/v1/zones/{zoneId}/records?type=TYPE
  // Lists all records of the given type in the zone.
  // WHY no &name= filter: zoneId already scopes the query to the exact zone;
  //   adding a placeholder name would force the operator to edit it before use.
  //   Omitting name returns all records of that type, which is the most useful
  //   default — the operator can append &name=... manually if needed.

  function buildGetBash(base, zoneId, type, dynamic, zone, token) {
    return (
      'curl -sk -X GET "' + base + '/api/v1/zones/' + zoneId +
      '/records?type=' + type + '" \\\n' +
      '  -H "Authorization: Bearer ' + token + '"'
    );
  }

  function buildGetCmd(base, zoneId, type, dynamic, zone, token) {
    return (
      'curl -sk -X GET "' + base + '/api/v1/zones/' + zoneId +
      '/records?type=' + type + '"' +
      ' -H "Authorization: Bearer ' + token + '"'
    );
  }

  function buildGetPs(base, zoneId, type, dynamic, zone, token) {
    return (
      'curl.exe -sk -X GET "' + base + '/api/v1/zones/' + zoneId +
      '/records?type=' + type + '" `\n' +
      '  -H "Authorization: Bearer ' + token + '"'
    );
  }

  // --- DELETE builders ---
  // DELETE /api/v1/zones/{zoneId}/records?name=NAME&type=TYPE
  // Deletes a record by name + type (the "delete by name" endpoint).
  // WHY by-name endpoint (not by-ID): the operator typically knows the record name
  //   but not the internal HE zone record ID. The by-name endpoint is more practical
  //   for automation scripts.
  // WHY zoneId in the path (not omitted): the API route is nested under /zones/{zoneID}/
  //   records — omitting it hits no route and returns 404 not_found. zoneId is passed
  //   from data-zone-id on the button (the numeric HE zone ID, e.g. "1110810").
  // WHY use recordName() for DELETE name:
  //   TXT → apex zone name (correct, no subdomain needed for SPF/DKIM etc.)
  //   A/AAAA/CNAME → {subdomain}.zone — the curly-brace placeholder makes it
  //   impossible to mistake for a real hostname, preventing accidental deletion
  //   of the wrong record if the operator forgets to fill in the subdomain.
  //
  // PREVIOUSLY WRONG: used /api/v1/records?name=... (missing /zones/{zoneId}/)
  //   which matched no route → always returned 404 not_found.

  function buildDeleteBash(base, zoneId, type, dynamic, zone, token) {
    var name = recordName(type, zone);
    return (
      'curl -sk -X DELETE "' + base + '/api/v1/zones/' + zoneId + '/records?name=' + name + '&type=' + type + '" \\\n' +
      '  -H "Authorization: Bearer ' + token + '"'
    );
  }

  function buildDeleteCmd(base, zoneId, type, dynamic, zone, token) {
    var name = recordName(type, zone);
    return (
      'curl -sk -X DELETE "' + base + '/api/v1/zones/' + zoneId + '/records?name=' + name + '&type=' + type + '"' +
      ' -H "Authorization: Bearer ' + token + '"'
    );
  }

  function buildDeletePs(base, zoneId, type, dynamic, zone, token) {
    var name = recordName(type, zone);
    return (
      'curl.exe -sk -X DELETE "' + base + '/api/v1/zones/' + zoneId + '/records?name=' + name + '&type=' + type + '" `\n' +
      '  -H "Authorization: Bearer ' + token + '"'
    );
  }

  // buildForShell dispatches to the correct builder based on the active shell tab and HTTP method.
  // WHY method stored on dialog.dataset: showCurlTemplate sets it from button's data-method attr;
  //   refreshCmd and setCurlTab can then rebuild without needing the originating button.
  function buildForShell(shell, method, base, zoneId, type, dynamic, zone) {
    var token = (document.getElementById('curl-token-input').value || '').trim();
    if (!token) token = 'YOUR_API_TOKEN';

    if (method === 'GET') {
      if (shell === 'cmd') return buildGetCmd(base, zoneId, type, dynamic, zone, token);
      if (shell === 'ps')  return buildGetPs(base, zoneId, type, dynamic, zone, token);
      return buildGetBash(base, zoneId, type, dynamic, zone, token);
    }
    if (method === 'DELETE') {
      if (shell === 'cmd') return buildDeleteCmd(base, zoneId, type, dynamic, zone, token);
      if (shell === 'ps')  return buildDeletePs(base, zoneId, type, dynamic, zone, token);
      return buildDeleteBash(base, zoneId, type, dynamic, zone, token);
    }
    // default: POST
    if (shell === 'cmd') return buildCmd(base, zoneId, type, dynamic, zone, token);
    if (shell === 'ps')  return buildPs(base, zoneId, type, dynamic, zone, token);
    return buildBash(base, zoneId, type, dynamic, zone, token);
  }

  // refreshCmd re-renders the <pre> when the token input or tab changes.
  function refreshCmd() {
    var dialog = document.getElementById('curl-dialog');
    if (!dialog || !dialog.open) return;
    var pre = document.getElementById('curl-dialog-cmd');
    if (!pre) return;
    pre.textContent = buildForShell(
      dialog.dataset.shell  || 'bash',
      dialog.dataset.method || 'POST',
      dialog.dataset.base,
      dialog.dataset.zoneId,
      dialog.dataset.type,
      dialog.dataset.dynamic === 'true',
      dialog.dataset.zone
    );
  }

  // setCurlTab switches the active tab and re-renders the command.
  // Exported on window so the onclick attributes in accounts.templ can reach it.
  window.setCurlTab = function (shell) {
    var dialog = document.getElementById('curl-dialog');
    dialog.dataset.shell = shell;

    // Update tab active class.
    ['bash', 'cmd', 'ps'].forEach(function (s) {
      var btn = document.getElementById('curl-tab-' + s);
      if (btn) btn.classList.toggle('curl-tab-active', s === shell);
    });

    refreshCmd();
  };

  // showCurlTemplate is called by onclick on each template button.
  // Exported on window so templ-generated onclick attributes can reach it.
  window.showCurlTemplate = function (btn) {
    var zone    = btn.dataset.zone;
    var zoneId  = btn.dataset.zoneId;
    var type    = btn.dataset.type;
    var dynamic = btn.dataset.dynamic === 'true';
    var method  = btn.dataset.method || 'POST';
    var base    = window.location.origin;

    var dialog = document.getElementById('curl-dialog');
    // Store current context on the dialog element so refreshCmd/setCurlTab can rebuild.
    dialog.dataset.base    = base;
    dialog.dataset.zone    = zone;
    dialog.dataset.zoneId  = zoneId;
    dialog.dataset.type    = type;
    dialog.dataset.dynamic = dynamic;
    dialog.dataset.method  = method;
    dialog.dataset.shell   = dialog.dataset.shell || 'bash'; // keep last-used tab

    var label = method + ' ' + type + (dynamic ? ' (dynamic DDNS)' : '') + ' \u2192 ' + zone;
    document.getElementById('curl-dialog-label').textContent = label;

    // Sync tab button state to match persisted shell.
    ['bash', 'cmd', 'ps'].forEach(function (s) {
      var tabBtn = document.getElementById('curl-tab-' + s);
      if (tabBtn) tabBtn.classList.toggle('curl-tab-active', s === (dialog.dataset.shell || 'bash'));
    });

    document.getElementById('curl-dialog-cmd').textContent = buildForShell(
      dialog.dataset.shell || 'bash', method, base, zoneId, type, dynamic, zone
    );

    dialog.showModal();
  };

  // showCopiedFeedback briefly changes the Copy button text and colour to confirm success.
  function showCopiedFeedback() {
    var btn = document.getElementById('curl-copy-btn');
    var orig = btn.textContent;
    btn.textContent = '\u2713 Copied!';
    btn.classList.add('btn-copied');
    setTimeout(function () {
      btn.textContent = orig;
      btn.classList.remove('btn-copied');
    }, 2000);
  }

  // fallbackCopy uses the legacy execCommand API for HTTP contexts where
  // navigator.clipboard is unavailable (requires secure context / HTTPS).
  // WHY needed: navigator.clipboard.writeText() is blocked on plain HTTP (non-localhost
  // in some browsers). execCommand('copy') works everywhere but is deprecated — used
  // only as a fallback when the modern API is absent or throws.
  function fallbackCopy(text) {
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    try { document.execCommand('copy'); showCopiedFeedback(); } catch (e) {}
    document.body.removeChild(ta);
  }

  // copyCurlCmd copies the current <pre> text to the clipboard.
  // Tries the modern Clipboard API first, falls back to execCommand on failure.
  window.copyCurlCmd = function () {
    var text = document.getElementById('curl-dialog-cmd').textContent;
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(showCopiedFeedback).catch(function () {
        fallbackCopy(text);
      });
    } else {
      fallbackCopy(text);
    }
  };

  // Wire token input → live command rebuild once DOM is ready.
  document.addEventListener('DOMContentLoaded', function () {
    var tokenInput = document.getElementById('curl-token-input');
    if (tokenInput) {
      tokenInput.addEventListener('input', refreshCmd);
    }
  });
})();

// ─── Password show/hide toggle ────────────────────────────────────────────────
// togglePw is called by onclick="togglePw(this)" on .pw-toggle buttons.
//
// HOW IT WORKS:
//   The button is always the immediate next sibling of the password input inside
//   a .pw-field wrapper. previousElementSibling gives the input without needing an id.
//   Toggling input.type between "password" and "text" reveals / hides the value.
//   The button innerHTML is swapped between an eye-open and eye-off SVG icon.
//
// WHY previousElementSibling (not a querySelector by id):
//   Multiple password fields can exist on the same page (e.g. current/new/confirm).
//   ID lookups would require unique IDs on every input; sibling traversal requires none.
//
// WHY SVG inline strings in JS (not CSS background-image):
//   SVG icon needs to change on every toggle click. Swapping innerHTML is simpler
//   than maintaining two CSS classes with separate background-image rules.

(function () {
  function eyeOnSvg() {
    // Eye open — shown when the field is type="password" (value hidden, click to reveal).
    return '<svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24"' +
      ' fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
      '<path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/>' +
      '<circle cx="12" cy="12" r="3"/>' +
      '</svg>';
  }

  function eyeOffSvg() {
    // Eye with slash — shown when the field is type="text" (value visible, click to hide).
    return '<svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24"' +
      ' fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
      '<path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/>' +
      '<path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/>' +
      '<path d="M6.34 6.34L17.66 17.66"/>' +
      '</svg>';
  }

  window.togglePw = function (btn) {
    var input = btn.previousElementSibling;
    if (!input) return;
    var show = (input.type === 'password');
    input.type = show ? 'text' : 'password';
    btn.innerHTML = show ? eyeOffSvg() : eyeOnSvg();
    btn.setAttribute('aria-label', show ? 'Hide password' : 'Show password');
  };
})();

// ─── Token reveal dialog ──────────────────────────────────────────────────────
// showRevealDialog, submitReveal, and copyRevealToken implement the "Show token"
// flow on the Tokens page. They are only reachable when TOKEN_RECOVERY_ENABLED=true
// (the Show button is not rendered otherwise — see TokenRow in tokens.templ).
//
// HOW IT WORKS:
//   1. Operator clicks "Show" on a token row → showRevealDialog(jti) opens the dialog.
//   2. Operator enters their portal password and clicks "Reveal token".
//   3. submitReveal() POSTs to /admin/tokens/{jti}/reveal with the password.
//   4. Server verifies the password and returns the decrypted raw token as plain text.
//   5. The token is displayed in a <pre> element inside the dialog.
//   6. Operator copies it with the Copy button, then closes the dialog.
//
// WHY fetch() not htmx for the reveal POST:
//   The password must be read from the input, the JTI is stored on the dialog (not an
//   htmx attribute on the button), and the response needs to update multiple elements
//   (hide the error, show the token, show the Copy button). fetch() gives precise control
//   over all three without a complex htmx hx-vals + OOB swap setup.

(function () {
  window.showRevealDialog = function (jti) {
    var dialog = document.getElementById('reveal-dialog');
    dialog.dataset.jti = jti;

    // Reset state from previous open.
    document.getElementById('reveal-password').value = '';
    document.getElementById('reveal-error').style.display = 'none';
    document.getElementById('reveal-error').textContent = '';
    document.getElementById('reveal-token-value').style.display = 'none';
    document.getElementById('reveal-token-value').textContent = '';
    document.getElementById('reveal-copy-btn').style.display = 'none';

    document.getElementById('reveal-dialog-label').textContent = jti.slice(0, 8) + '...';
    dialog.showModal();
    // Auto-focus password field for keyboard UX.
    setTimeout(function () { document.getElementById('reveal-password').focus(); }, 50);
  };

  window.submitReveal = function () {
    var dialog = document.getElementById('reveal-dialog');
    var jti = dialog.dataset.jti;
    var password = document.getElementById('reveal-password').value;
    var errEl = document.getElementById('reveal-error');
    var preEl = document.getElementById('reveal-token-value');
    var copyBtn = document.getElementById('reveal-copy-btn');

    errEl.style.display = 'none';
    errEl.textContent = '';

    var body = new URLSearchParams();
    body.append('password', password);

    fetch('/admin/tokens/' + jti + '/reveal', { method: 'POST', body: body })
      .then(function (resp) {
        if (!resp.ok) {
          return resp.text().then(function (msg) {
            errEl.textContent = msg || 'Failed to reveal token.';
            errEl.style.display = 'inline';
          });
        }
        return resp.text().then(function (rawToken) {
          preEl.textContent = rawToken;
          preEl.style.display = 'block';
          copyBtn.style.display = 'inline-block';
        });
      })
      .catch(function () {
        errEl.textContent = 'Network error — could not reach the server.';
        errEl.style.display = 'inline';
      });
  };

  window.copyRevealToken = function () {
    var text = document.getElementById('reveal-token-value').textContent;
    var btn = document.getElementById('reveal-copy-btn');
    var orig = btn.textContent;
    var done = function () {
      btn.textContent = '\u2713 Copied!';
      btn.classList.add('btn-copied');
      setTimeout(function () { btn.textContent = orig; btn.classList.remove('btn-copied'); }, 2000);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done).catch(function () {
        fallbackCopyText(text, done);
      });
    } else {
      fallbackCopyText(text, done);
    }
  };

  function fallbackCopyText(text, cb) {
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed'; ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.focus(); ta.select();
    try { document.execCommand('copy'); cb(); } catch (e) {}
    document.body.removeChild(ta);
  }

  // Submit on Enter key in the password field.
  document.addEventListener('DOMContentLoaded', function () {
    var pwInput = document.getElementById('reveal-password');
    if (pwInput) {
      pwInput.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') { e.preventDefault(); window.submitReveal(); }
      });
    }
  });
})();
