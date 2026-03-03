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

(function () {
  // buildCmd constructs the curl command string for the given parameters.
  // Uses the token currently in #curl-token-input (falls back to placeholder).
  function buildCmd(base, zoneId, type, dynamic, zone) {
    var token = (document.getElementById('curl-token-input').value || '').trim();
    if (!token) token = 'YOUR_API_TOKEN';

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

    var bodyStr = JSON.stringify(body, null, 2);
    var cmd = (
      'curl -s -X POST ' + base + '/api/v1/zones/' + zoneId + '/records \\\n' +
      '  -H "Authorization: Bearer ' + token + '" \\\n' +
      '  -H "Content-Type: application/json" \\\n' +
      "  -d '" + bodyStr + "'"
    );
    // Append DDNS update usage note for dynamic A records.
    // WHY appended here not in buildCmd: only A+ has a DDNS key workflow;
    // other types do not use dyn.dns.he.net and would show irrelevant info.
    if (dynamic) {
      cmd += '\n\n# Response contains ddns_key \u2014 use it to push updates:\n' +
             '# curl "https://dyn.dns.he.net/nic/update' +
             '?hostname=subdomain.' + zone + '&password=YOUR_DDNS_KEY&myip=1.2.3.4"';
    }
    return cmd;
  }

  // refreshCmd re-renders the <pre> in the open dialog — called on token input change.
  function refreshCmd() {
    var dialog = document.getElementById('curl-dialog');
    if (!dialog || !dialog.open) return;
    var pre = document.getElementById('curl-dialog-cmd');
    if (!pre) return;
    pre.textContent = buildCmd(
      dialog.dataset.base,
      dialog.dataset.zoneId,
      dialog.dataset.type,
      dialog.dataset.dynamic === 'true',
      dialog.dataset.zone
    );
  }

  // showCurlTemplate is called by onclick on each template button.
  // Exported on window so templ-generated onclick attributes can reach it.
  window.showCurlTemplate = function (btn) {
    var zone    = btn.dataset.zone;
    var zoneId  = btn.dataset.zoneId;
    var type    = btn.dataset.type;
    var dynamic = btn.dataset.dynamic === 'true';
    var base    = window.location.origin;

    var dialog = document.getElementById('curl-dialog');
    // Store current context on the dialog element so refreshCmd can rebuild on token change.
    dialog.dataset.base    = base;
    dialog.dataset.zone    = zone;
    dialog.dataset.zoneId  = zoneId;
    dialog.dataset.type    = type;
    dialog.dataset.dynamic = dynamic;

    var label = type + (dynamic ? ' (dynamic DDNS)' : '') + ' \u2192 ' + zone;
    document.getElementById('curl-dialog-label').textContent = label;
    document.getElementById('curl-dialog-cmd').textContent = buildCmd(base, zoneId, type, dynamic, zone);

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
