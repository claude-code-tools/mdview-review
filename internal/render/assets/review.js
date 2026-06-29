(function () {
  var TOKEN = "__MDVIEW_TOKEN__";
  var bar = document.getElementById("mdview-bar");
  var approve = document.getElementById("mdview-approve");
  var changes = document.getElementById("mdview-changes");
  var panel = document.getElementById("mdview-panel");
  var comment = document.getElementById("mdview-comment");
  var submit = document.getElementById("mdview-submit");
  var cancel = document.getElementById("mdview-cancel");
  var done = false;
  try { new EventSource("/events"); } catch (e) {}
  function setStatus(msg) {
    bar.innerHTML = "";
    var s = document.createElement("div");
    s.id = "mdview-status"; s.textContent = msg; bar.appendChild(s);
  }
  function send(payload) {
    if (done) return; done = true;
    fetch("/verdict", {
      method: "POST",
      headers: { "Content-Type": "application/json", "Authorization": "Bearer " + TOKEN },
      body: JSON.stringify(payload)
    }).then(function (r) {
      if (r.ok) setStatus("✓ Sent — you can close this tab.");
      else { done = false; setStatus("Could not send (HTTP " + r.status + ")."); }
    }).catch(function () { setStatus("Review session ended."); });
  }
  approve.addEventListener("click", function () { send({ verdict: "approve" }); });
  changes.addEventListener("click", function () {
    panel.classList.add("open"); approve.style.display = "none"; changes.style.display = "none";
    comment.focus();
  });
  cancel.addEventListener("click", function () {
    panel.classList.remove("open"); approve.style.display = ""; changes.style.display = "";
  });
  comment.addEventListener("input", function () { submit.disabled = comment.value.trim() === ""; });
  function doSubmit() { var c = comment.value.trim(); if (c) send({ verdict: "changes", comment: c }); }
  submit.addEventListener("click", doSubmit);
  comment.addEventListener("keydown", function (e) {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") { e.preventDefault(); doSubmit(); }
    if (e.key === "Escape") { cancel.click(); }
  });
})();
