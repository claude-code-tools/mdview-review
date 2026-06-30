(function () {
  var TOKEN = "__MDVIEW_TOKEN__";
  var COMMANDS = __MDVIEW_COMMANDS__;
  var bar = document.getElementById("mdview-bar");
  var approve = document.getElementById("mdview-approve");
  var changes = document.getElementById("mdview-changes");
  var comment = document.getElementById("mdview-comment");
  var submit = document.getElementById("mdview-submit");
  var cancel = document.getElementById("mdview-cancel");
  var done = false;

  // Keep-alive (so the server can detect a closed tab) + reload-on-reconnect: if the SSE
  // reattaches to a NEW server instance (different nonce), the doc changed across a review
  // round — reload to pick up the fresh page and token.
  var seenNonce = null;
  try {
    var es = new EventSource("/events");
    es.addEventListener("hello", function (e) {
      if (seenNonce === null) seenNonce = e.data;
      else if (e.data !== seenNonce) location.reload();
    });
  } catch (e) {}

  var CHECK = '<svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M8 0a8 8 0 1 1 0 16A8 8 0 0 1 8 0Zm3.78 5.97a.75.75 0 0 0-1.06 0L7 9.69 5.28 7.97a.75.75 0 0 0-1.06 1.06l2.25 2.25a.75.75 0 0 0 1.06 0l4.25-4.25a.75.75 0 0 0 0-1.06Z"/></svg>';

  function finish(msg, ok) {
    bar.classList.remove("panel-open");
    bar.classList.add("mv-done");
    var span = document.createElement("span");
    span.textContent = msg;
    bar.innerHTML = "";
    var wrap = document.createElement("div");
    wrap.className = "mv-confirm";
    if (ok) wrap.innerHTML = CHECK;
    wrap.appendChild(span);
    bar.appendChild(wrap);
  }

  function send(payload) {
    if (done) return;
    done = true;
    fetch("/verdict", {
      method: "POST",
      headers: { "Content-Type": "application/json", "Authorization": "Bearer " + TOKEN },
      body: JSON.stringify(payload)
    }).then(function (r) {
      if (r.ok) finish("Sent — you can close this tab.", true);
      else finish("Couldn't record your response — please re-run.", false);
    }).catch(function () {
      finish("Review session ended.", false);
    });
  }

  var cmdStrip = document.getElementById("mdview-commands");
  (function buildCommands() {
    if (!cmdStrip || !Array.isArray(COMMANDS) || COMMANDS.length === 0) {
      if (cmdStrip) cmdStrip.style.display = "none";
      return;
    }
    COMMANDS.forEach(function (cmd) {
      if (!cmd || !cmd.id || !cmd.label) return;
      var b = document.createElement("button");
      b.type = "button";
      b.className = "mv-cmd" + (cmd.recommended ? " mv-cmd--recommended" : "");
      b.textContent = cmd.label;
      b.addEventListener("click", function () {
        send({ verdict: "command", command: cmd.id, prompt: cmd.prompt || "" });
      });
      cmdStrip.appendChild(b);
    });
  })();

  approve.addEventListener("click", function () { send({ verdict: "approve" }); });
  changes.addEventListener("click", function () {
    bar.classList.add("panel-open");
    comment.focus();
  });
  cancel.addEventListener("click", function () { bar.classList.remove("panel-open"); });
  comment.addEventListener("input", function () { submit.disabled = comment.value.trim() === ""; });

  function doSubmit() {
    var c = comment.value.trim();
    if (c) send({ verdict: "changes", comment: c });
  }
  submit.addEventListener("click", doSubmit);
  comment.addEventListener("keydown", function (e) {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") { e.preventDefault(); doSubmit(); }
    if (e.key === "Escape") { bar.classList.remove("panel-open"); }
  });
})();
