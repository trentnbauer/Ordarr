// Colour theme: Auto follows the system, Light and Dark override it. The saved choice is applied
// before the page paints (this script is not deferred) so there is no flash of the wrong theme.
(function () {
  var KEY = "ordarr-theme", root = document.documentElement;
  var order = ["auto", "light", "dark"], names = { auto: "Auto", light: "Light", dark: "Dark" };
  function stored() {
    try { var v = localStorage.getItem(KEY); return names[v] ? v : "auto"; } catch (e) { return "auto"; }
  }
  function apply(theme) {
    if (theme === "light" || theme === "dark") root.setAttribute("data-theme", theme);
    else root.removeAttribute("data-theme");
  }
  apply(stored());
  document.addEventListener("DOMContentLoaded", function () {
    var button = document.getElementById("theme-toggle");
    if (!button) return;
    function label() {
      var theme = stored();
      button.textContent = "Theme: " + names[theme];
      button.title = theme === "auto" ? "Following your system. Click to choose Light, then Dark." : "Click to change the colour theme.";
    }
    button.addEventListener("click", function () {
      var next = order[(order.indexOf(stored()) + 1) % order.length];
      try { localStorage.setItem(KEY, next); } catch (e) {}
      apply(next);
      label();
    });
    label();
  });
})();
