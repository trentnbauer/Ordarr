// Shows a spinner next to Activity while Ordarr is scanning, syncing or requesting, and keeps it
// up to date without a page reload.
(function () {
  var spinner = document.getElementById("activity-spinner");
  if (!spinner || !window.fetch) return;
  var text = spinner.querySelector(".visually-hidden");
  // Derived from the sidebar link so it keeps working under a URL base path (ORDARR_BASE_URL).
  var link = spinner.closest("a");
  if (!link) return;
  var url = link.getAttribute("href").replace(/jobs$/, "activity/busy");
  function show(state) {
    spinner.hidden = !state.busy;
    spinner.title = state.label || "";
    if (text) text.textContent = state.label || "";
  }
  function poll() {
    if (document.hidden) return;
    fetch(url, { credentials: "same-origin", headers: { Accept: "application/json" } })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (state) { if (state) show(state); })
      .catch(function () {});
  }
  setInterval(poll, 4000);
  document.addEventListener("visibilitychange", poll);
})();
