(function () {
  "use strict";

  try {
    window.localStorage.setItem("isLoggedIn", "true");
  } catch (_) {
    // Ignore storage errors; the management API itself remains keyless.
  }

  var USAGE_HASH = "#/usage";
  var AUTO_CALIBRATION_TITLE = "Automatic Calibration";
  var REQUEST_EVENTS_TITLE = "Request Events";
  var USAGE_PAGE_TITLE = "Usage Statistics";
  var AUTH_FILES_PAGE_TITLE = "Auth Files";
  var CLAUDE_CLOAK_PANEL_ID = "cliproxy-claude-cloak-panel";
  var CLAUDE_CLOAK_STYLE_ID = "cliproxy-claude-cloak-style";
  var authFilesCache = null;
  var authFilesLoadedAt = 0;
  var authFilesLoading = false;

  function normalizedText(el) {
    return (el && el.textContent || "").replace(/\s+/g, " ").trim();
  }

  function findLabel(label) {
    var candidates = document.querySelectorAll("h1,h2,h3,h4,h5,h6,div,span");
    for (var i = 0; i < candidates.length; i++) {
      if (normalizedText(candidates[i]) === label) {
        return candidates[i];
      }
    }
    return null;
  }

  function findCard(label) {
    var labelEl = findLabel(label);
    if (!labelEl) {
      return null;
    }

    var node = labelEl;
    var best = null;
    for (var depth = 0; node && depth < 10; depth++) {
      var parent = node.parentElement;
      if (!parent) {
        break;
      }
      var rect = node.getBoundingClientRect();
      var parentRect = parent.getBoundingClientRect();
      if (
        parent.children.length > 1 &&
        parentRect.width >= Math.max(280, rect.width) &&
        parentRect.height > rect.height + 24
      ) {
        best = node;
      }
      node = parent;
    }
    return best;
  }

  function findUsageContainer() {
    var headings = document.querySelectorAll("h1");
    for (var i = 0; i < headings.length; i++) {
      if (normalizedText(headings[i]) === USAGE_PAGE_TITLE) {
        return (
          headings[i].closest('[class*="UsagePage-module__container"]') ||
          headings[i].parentElement
        );
      }
    }
    return null;
  }

  function findPageContainer(title) {
    var headings = document.querySelectorAll("h1");
    for (var i = 0; i < headings.length; i++) {
      if (normalizedText(headings[i]) === title) {
        return (
          headings[i].closest('[class*="Page-module__container"]') ||
          headings[i].parentElement
        );
      }
    }
    return null;
  }

  function findDirectUsageChild(label) {
    var labelEl = findLabel(label);
    var container = findUsageContainer();
    if (!labelEl || !container) {
      return null;
    }

    var node = labelEl;
    while (node && node.parentElement && node.parentElement !== container) {
      node = node.parentElement;
    }
    return node && node.parentElement === container ? node : null;
  }

  function moveRequestEventsBelowUsageHeader() {
    var container = findUsageContainer();
    var requestEvents = findDirectUsageChild(REQUEST_EVENTS_TITLE);
    if (!container || !requestEvents) {
      return;
    }

    var header = container.querySelector('[class*="UsagePage-module__header"]');
    if (!header || header.parentElement !== container) {
      return;
    }

    if (header.nextElementSibling !== requestEvents) {
      container.insertBefore(requestEvents, header.nextElementSibling);
    }
  }

  function adjustUsageDetails() {
    if (window.location.hash !== USAGE_HASH) {
      return;
    }

    var automaticCalibration = findCard(AUTO_CALIBRATION_TITLE);

    if (automaticCalibration) {
      automaticCalibration.remove();
    }

    moveRequestEventsBelowUsageHeader();
  }

  function authFileSupportsClaudeCloak(file) {
    var provider = String(file.provider || file.type || "").trim().toLowerCase();
    return provider === "claude" || provider === "anthropic";
  }

  function normalizeCloakMode(value) {
    value = String(value || "").trim().toLowerCase();
    if (value === "always" || value === "full") {
      return "always";
    }
    if (value === "never" || value === "off" || value === "false" || value === "disabled") {
      return "never";
    }
    return "auto";
  }

  function ensureClaudeCloakStyles() {
    if (document.getElementById(CLAUDE_CLOAK_STYLE_ID)) {
      return;
    }
    var style = document.createElement("style");
    style.id = CLAUDE_CLOAK_STYLE_ID;
    style.textContent =
      "#" + CLAUDE_CLOAK_PANEL_ID + "{" +
      "margin:0 0 16px;padding:14px 16px;border:1px solid var(--border-color);" +
      "border-radius:8px;background:var(--bg-secondary);color:var(--text-primary);" +
      "display:flex;flex-direction:column;gap:10px" +
      "}" +
      "#" + CLAUDE_CLOAK_PANEL_ID + " .ccp-head{" +
      "display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap" +
      "}" +
      "#" + CLAUDE_CLOAK_PANEL_ID + " .ccp-title{font-size:14px;font-weight:700}" +
      "#" + CLAUDE_CLOAK_PANEL_ID + " .ccp-refresh{" +
      "border:1px solid var(--border-color);background:var(--bg-primary);color:var(--text-primary);" +
      "border-radius:6px;padding:5px 10px;font:inherit;font-size:12px;cursor:pointer" +
      "}" +
      "#" + CLAUDE_CLOAK_PANEL_ID + " .ccp-refresh:hover{border-color:var(--primary-color)}" +
      "#" + CLAUDE_CLOAK_PANEL_ID + " .ccp-list{display:flex;flex-direction:column;gap:8px}" +
      "#" + CLAUDE_CLOAK_PANEL_ID + " .ccp-row{" +
      "display:grid;grid-template-columns:minmax(180px,1fr) auto;gap:12px;align-items:center;" +
      "padding:10px;border:1px solid var(--border-color);border-radius:8px;background:var(--bg-primary)" +
      "}" +
      "#" + CLAUDE_CLOAK_PANEL_ID + " .ccp-name{font-size:13px;font-weight:700;word-break:break-word}" +
      "#" + CLAUDE_CLOAK_PANEL_ID + " .ccp-meta{font-size:12px;color:var(--text-secondary);word-break:break-word}" +
      "#" + CLAUDE_CLOAK_PANEL_ID + " .ccp-controls{display:flex;align-items:center;gap:12px;flex-wrap:wrap;justify-content:flex-end}" +
      "#" + CLAUDE_CLOAK_PANEL_ID + " .ccp-toggle{display:inline-flex;align-items:center;gap:6px;font-size:12px;color:var(--text-primary);white-space:nowrap}" +
      "#" + CLAUDE_CLOAK_PANEL_ID + " input{accent-color:var(--primary-color);width:16px;height:16px}" +
      "#" + CLAUDE_CLOAK_PANEL_ID + " .ccp-status{font-size:12px;color:var(--text-secondary);min-height:16px}" +
      "#" + CLAUDE_CLOAK_PANEL_ID + " .ccp-error{color:var(--error-color)}" +
      "@media(max-width:768px){#" + CLAUDE_CLOAK_PANEL_ID + " .ccp-row{grid-template-columns:1fr}#" +
      CLAUDE_CLOAK_PANEL_ID + " .ccp-controls{justify-content:flex-start}}";
    document.head.appendChild(style);
  }

  function currentAuthFilesPanelContainer() {
    var container = findPageContainer(AUTH_FILES_PAGE_TITLE);
    if (!container) {
      return null;
    }
    var header = container.querySelector('[class*="AuthPage-module__header"],[class*="header"]');
    if (!header || header.parentElement !== container) {
      var heading = container.querySelector("h1");
      header = heading && heading.parentElement === container ? heading : null;
    }
    return { container: container, header: header };
  }

  function setPanelStatus(panel, message, isError) {
    var status = panel.querySelector(".ccp-status");
    if (!status) {
      return;
    }
    status.textContent = message || "";
    status.classList.toggle("ccp-error", !!isError);
  }

  function patchClaudeCloakField(fileName, fields, panel) {
    var body = { name: fileName };
    Object.keys(fields).forEach(function (key) {
      body[key] = fields[key];
    });
    setPanelStatus(panel, "Saving...", false);
    return fetch("/v0/management/auth-files/fields", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    })
      .then(function (res) {
        if (!res.ok) {
          return res.text().then(function (text) {
            throw new Error(text || res.statusText);
          });
        }
      })
      .then(function () {
        authFilesLoadedAt = 0;
        setPanelStatus(panel, "Saved", false);
        loadAuthFiles(true);
      })
      .catch(function (err) {
        setPanelStatus(panel, err && err.message ? err.message : "Update failed", true);
        loadAuthFiles(true);
      });
  }

  function createToggle(label, checked, disabled, onChange) {
    var wrapper = document.createElement("label");
    wrapper.className = "ccp-toggle";
    var input = document.createElement("input");
    input.type = "checkbox";
    input.checked = !!checked;
    input.disabled = !!disabled;
    input.addEventListener("change", function () {
      onChange(input.checked);
    });
    var text = document.createElement("span");
    text.textContent = label;
    wrapper.appendChild(input);
    wrapper.appendChild(text);
    return wrapper;
  }

  function renderClaudeCloakPanel(files) {
    var location = currentAuthFilesPanelContainer();
    if (!location) {
      var existing = document.getElementById(CLAUDE_CLOAK_PANEL_ID);
      if (existing) {
        existing.remove();
      }
      return;
    }

    ensureClaudeCloakStyles();

    var panel = document.getElementById(CLAUDE_CLOAK_PANEL_ID);
    if (!panel) {
      panel = document.createElement("section");
      panel.id = CLAUDE_CLOAK_PANEL_ID;
    }
    if (location.header && location.header.nextElementSibling !== panel) {
      location.container.insertBefore(panel, location.header.nextElementSibling);
    } else if (!location.header && location.container.firstChild !== panel) {
      location.container.insertBefore(panel, location.container.firstChild);
    }

    panel.innerHTML = "";
    var head = document.createElement("div");
    head.className = "ccp-head";
    var title = document.createElement("div");
    title.className = "ccp-title";
    title.textContent = "Claude Cloaking";
    var refresh = document.createElement("button");
    refresh.type = "button";
    refresh.className = "ccp-refresh";
    refresh.textContent = "Refresh";
    refresh.addEventListener("click", function () {
      loadAuthFiles(true);
    });
    head.appendChild(title);
    head.appendChild(refresh);

    var list = document.createElement("div");
    list.className = "ccp-list";
    var claudeFiles = (files || []).filter(authFileSupportsClaudeCloak);
    if (claudeFiles.length === 0) {
      var empty = document.createElement("div");
      empty.className = "ccp-meta";
      empty.textContent = "No Claude auth files found.";
      list.appendChild(empty);
    }

    claudeFiles.forEach(function (file) {
      var mode = normalizeCloakMode(file.cloak_mode);
      var fileName = file.name || file.id || "";
      var row = document.createElement("div");
      row.className = "ccp-row";

      var identity = document.createElement("div");
      var name = document.createElement("div");
      name.className = "ccp-name";
      name.textContent = file.label || file.account || file.email || fileName;
      var meta = document.createElement("div");
      meta.className = "ccp-meta";
      meta.textContent = fileName + " - mode: " + mode;
      identity.appendChild(name);
      identity.appendChild(meta);

      var controls = document.createElement("div");
      controls.className = "ccp-controls";
      controls.appendChild(
        createToggle("Cloak", mode !== "never", file.disabled, function (checked) {
          patchClaudeCloakField(fileName, { cloak_mode: checked ? "auto" : "never" }, panel);
        })
      );
      controls.appendChild(
        createToggle("Everything", mode === "always", file.disabled, function (checked) {
          patchClaudeCloakField(fileName, { cloak_mode: checked ? "always" : "auto" }, panel);
        })
      );
      controls.appendChild(
        createToggle("Strict Mode", !!file.cloak_strict_mode, file.disabled, function (checked) {
          patchClaudeCloakField(fileName, { cloak_strict_mode: checked }, panel);
        })
      );

      row.appendChild(identity);
      row.appendChild(controls);
      list.appendChild(row);
    });

    var status = document.createElement("div");
    status.className = "ccp-status";
    panel.appendChild(head);
    panel.appendChild(list);
    panel.appendChild(status);
  }

  function loadAuthFiles(force) {
    if (!currentAuthFilesPanelContainer()) {
      return;
    }
    if (!force && authFilesCache && Date.now() - authFilesLoadedAt < 5000) {
      renderClaudeCloakPanel(authFilesCache);
      return;
    }
    if (authFilesLoading) {
      return;
    }
    authFilesLoading = true;
    fetch("/v0/management/auth-files")
      .then(function (res) {
        if (!res.ok) {
          throw new Error(res.statusText || "failed to load auth files");
        }
        return res.json();
      })
      .then(function (data) {
        authFilesCache = data.files || [];
        authFilesLoadedAt = Date.now();
        renderClaudeCloakPanel(authFilesCache);
      })
      .catch(function () {
        renderClaudeCloakPanel(authFilesCache || []);
      })
      .finally(function () {
        authFilesLoading = false;
      });
  }

  function adjustAuthFilesDetails() {
    loadAuthFiles(false);
  }

  var scheduled = false;
  function scheduleAdjust() {
    if (scheduled) {
      return;
    }
    scheduled = true;
    window.requestAnimationFrame(function () {
      scheduled = false;
      adjustUsageDetails();
      adjustAuthFilesDetails();
    });
  }

  window.addEventListener("hashchange", scheduleAdjust);
  window.addEventListener("load", scheduleAdjust);

  var observer = new MutationObserver(scheduleAdjust);
  observer.observe(document.documentElement, { childList: true, subtree: true });

  scheduleAdjust();
})();
