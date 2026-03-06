// admin.js — curl template dialog for the Accounts page.
//
// WHY a separate JS file (not inline in the templ template):
//   templ escapes content inside <script> tags by default, making inline JS fragile.
//   A static file served by the embedded FS is the canonical pattern for this project.
//   DEPENDENCY: admin.js must be added to the go:embed directive in static.go and
//   referenced by a <script src> tag in layout.templ.
//
// HOW the dialog works:
//   Each template button carries data-zone, data-zone-id, data-type, data-dynamic attrs.
//   showCurlTemplate(btn) reads those attrs, builds the curl command, and opens the
//   shared <dialog id="curl-dialog"> (rendered once in AccountsPage).
//   The token input inside the dialog persists between openings because <dialog> stays
//   in the DOM — the user pastes their token once and all subsequent templates use it.
//   Three tabs (bash | cmd | PowerShell) switch the displayed command variant.

(function () {
  // bodyForType returns the request body object for a given record type.
  function bodyForType(type, dynamic, zone) {
    var body;
    if (type === 'A') {
      body = { type: 'A', name: 'subdomain.' + zone, content: '1.2.3.4', ttl: 300 };
      if (dynamic) {
        body.dynamic = true;
        body.ddns_key = 'optional-own-key';  // omit to auto-generate
      }
    } else if (type === 'AAAA') {
      body = { type: 'AAAA', name: 'subdomain.' + zone, content: '2001:db8::1', ttl: 3600 };
    } else if (type === 'CNAME') {
      body = { type: 'CNAME', name: 'subdomain.' + zone, content: 'target.example.com', ttl: 3600 };
    } else if (type === 'TXT') {
      // TXT records are typically set on the apex zone name, not a subdomain.
      body = { type: 'TXT', name: zone, content: 'v=spf1 ~all', ttl: 300 };
    }
    return body;
  }

  // buildBash generates the bash/Linux curl command (multi-line, single-quoted JSON body).
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

  // buildCmd generates the Windows CMD command (single line, inner quotes escaped as \").
  // WHY single line: CMD line continuation (^) is fragile; one line is more paste-friendly.
  // WHY \" escaping: CMD requires \" to embed literal double quotes inside a double-quoted string.
  function buildCmd(base, zoneId, type, dynamic, zone, token) {
    var body = bodyForType(type, dynamic, zone);
    // Escape inner double quotes for CMD: " → \"
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

  // buildPs generates the PowerShell command (multi-line with backtick, single-quoted JSON body).
  // WHY curl.exe not curl: in PowerShell, `curl` is an alias for Invoke-WebRequest which has
  //   different argument syntax. curl.exe invokes the real curl binary directly.
  // WHY single-quoted JSON: PowerShell single quotes are literal — no escaping needed.
  //   Double-quoted strings in PowerShell expand $variables, breaking JSON values like "1.2.3.4".
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

  // buildForShell dispatches to the correct builder based on the active shell tab.
  function buildForShell(shell, base, zoneId, type, dynamic, zone) {
    var token = (document.getElementById('curl-token-input').value || '').trim();
    if (!token) token = 'YOUR_API_TOKEN';
    if (shell === 'cmd')  return buildCmd(base, zoneId, type, dynamic, zone, token);
    if (shell === 'ps')   return buildPs(base, zoneId, type, dynamic, zone, token);
    return buildBash(base, zoneId, type, dynamic, zone, token);
  }

  // refreshCmd re-renders the <pre> when the token input changes.
  function refreshCmd() {
    var dialog = document.getElementById('curl-dialog');
    if (!dialog || !dialog.open) return;
    var pre = document.getElementById('curl-dialog-cmd');
    if (!pre) return;
    pre.textContent = buildForShell(
      dialog.dataset.shell || 'bash',
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
    var base    = window.location.origin;

    var dialog = document.getElementById('curl-dialog');
    // Store current context on the dialog element so refreshCmd/setCurlTab can rebuild.
    dialog.dataset.base    = base;
    dialog.dataset.zone    = zone;
    dialog.dataset.zoneId  = zoneId;
    dialog.dataset.type    = type;
    dialog.dataset.dynamic = dynamic;
    dialog.dataset.shell   = dialog.dataset.shell || 'bash'; // keep last-used tab

    var label = type + (dynamic ? ' (dynamic DDNS)' : '') + ' \u2192 ' + zone;
    document.getElementById('curl-dialog-label').textContent = label;

    // Sync tab button state to match persisted shell.
    ['bash', 'cmd', 'ps'].forEach(function (s) {
      var tabBtn = document.getElementById('curl-tab-' + s);
      if (tabBtn) tabBtn.classList.toggle('curl-tab-active', s === (dialog.dataset.shell || 'bash'));
    });

    document.getElementById('curl-dialog-cmd').textContent = buildForShell(
      dialog.dataset.shell || 'bash', base, zoneId, type, dynamic, zone
    );

    dialog.showModal();
  };

  // showCopiedFeedback briefly changes the Copy button text and colour to confirm success.
  function showCopiedFeedback() {
    var btn = document.getElementById('curl-copy-btn');
    var orig = btn.textContent;
    btn.textContent = '✓ Copied!';
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
