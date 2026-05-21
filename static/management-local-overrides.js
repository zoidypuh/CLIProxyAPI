(function () {
  "use strict";

  try {
    window.localStorage.setItem("isLoggedIn", "true");
  } catch (_) {
    // Ignore storage errors; the management API itself remains keyless.
  }

  var USAGE_HASH = "#/usage";
  var REQUEST_EVENTS_HASH = "#/request-events";
  var AUTO_CALIBRATION_TITLE = "Automatic Calibration";
  var REQUEST_EVENTS_TITLE = "Request Events";
  var CHART_LINES_TITLE = "Lines to display";
  var USAGE_PAGE_TITLE = "Usage Statistics";
  var AUTH_FILES_PAGE_TITLE = "Auth Files";
  var REQUEST_EVENTS_STYLE_ID = "cliproxy-request-events-style";
  var REQUEST_EVENTS_CARD_ID = "cliproxy-request-events-card";
  var REQUEST_EVENTS_HEADER_CONTROLS_ID = "cliproxy-request-events-header-controls";
  var REQUEST_EVENTS_PAGER_ID = "cliproxy-request-events-pager";
  var REQUEST_EVENTS_TOP_ROW_ID = "cliproxy-request-events-top-row";
  var REQUEST_EVENTS_COPY_FALLBACK_ID = "cliproxy-request-events-copy-fallback";
  var REQUEST_EVENTS_PAGE_SIZE = 100;
  var REQUEST_EVENTS_BOOT_FLAG = "cliproxyRequestEventsBoot";
  var REQUEST_EVENTS_LOG_ROOT = "\\\\wsl.localhost\\Ubuntu-24.04\\home\\gismar\\.cli-proxy-api\\logs";
  var CHART_LINES_REFRESH_ID = "cliproxy-chart-lines-refresh";
  var CHART_LINES_OBSERVER_FLAG = "cliproxyChartLinesObserver";
  var CLAUDE_CLOAK_PANEL_ID = "cliproxy-claude-cloak-panel";
  var CLAUDE_CLOAK_STYLE_ID = "cliproxy-claude-cloak-style";
  var REQUEST_EVENTS_ICON_SVG =
    '<svg class="cliproxy-request-events-nav-icon" width="18" height="18" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">' +
    '<path d="M5 6h10" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>' +
    '<path d="M5 12h8" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>' +
    '<path d="M5 18h6" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>' +
    '<circle cx="18" cy="17" r="3" stroke="currentColor" stroke-width="2"/>' +
    '<path d="M18 15.6V17l1 1" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>' +
    "</svg>";
  var authFilesCache = null;
  var authFilesLoadedAt = 0;
  var authFilesLoading = false;
  var initialRequestEventsRoute = window.location.hash === REQUEST_EVENTS_HASH;
  var requestEventsMode = initialRequestEventsRoute;
  var requestEventsPageIndex = 0;
  var requestEventsRowsSignature = "";

  if (initialRequestEventsRoute) {
    try {
      window.sessionStorage.setItem(REQUEST_EVENTS_BOOT_FLAG, "1");
    } catch (_) {
      // Ignore storage errors; the in-memory route flag still covers normal navigation.
    }
    window.location.hash = USAGE_HASH;
  }

  function looksLikeRouteIdentifier(value) {
    return /^(GET|POST|PUT|PATCH|DELETE|OPTIONS|HEAD)\s+\//.test(
      String(value || "").replace(/\s+/g, " ").trim()
    );
  }

  function clearRouteAPIKey(detail) {
    if (!detail || typeof detail !== "object") {
      return;
    }
    ["api_key", "apiKey", "APIKey"].forEach(function (key) {
      if (looksLikeRouteIdentifier(detail[key])) {
        delete detail[key];
      }
    });
  }

  function isLocalUsageSessionLabel(value) {
    value = String(value || "").trim();
    if (!value || value.length > 32) {
      return false;
    }
    if (/^(sk-|nvapi-|eyj|rt_|aiza|venice_)/i.test(value)) {
      return false;
    }
    return /^[a-z0-9_-]+$/i.test(value);
  }

  function normalizeUsageSession(detail) {
    if (!detail || typeof detail !== "object") {
      return;
    }
    var apiKey = detail.api_key || detail.apiKey || detail.APIKey || "";
    if (isLocalUsageSessionLabel(apiKey)) {
      detail.session_id = apiKey;
    }
  }

  function looksLikeUUIDSessionID(value) {
    return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
      String(value || "").trim()
    );
  }

  function mergeUsageAPI(target, source, routeKey) {
    target.total_requests = Number(target.total_requests || 0) + Number(source.total_requests || 0);
    target.total_tokens = Number(target.total_tokens || 0) + Number(source.total_tokens || 0);
    target.models = target.models || {};

    var models = source.models && typeof source.models === "object" ? source.models : {};
    Object.keys(models).forEach(function (modelName) {
      var sourceModel = models[modelName] && typeof models[modelName] === "object" ? models[modelName] : {};
      var targetModel = target.models[modelName] || { total_requests: 0, total_tokens: 0, details: [] };
      targetModel.total_requests =
        Number(targetModel.total_requests || 0) + Number(sourceModel.total_requests || 0);
      targetModel.total_tokens =
        Number(targetModel.total_tokens || 0) + Number(sourceModel.total_tokens || 0);
      targetModel.details = Array.isArray(targetModel.details) ? targetModel.details : [];
      (Array.isArray(sourceModel.details) ? sourceModel.details : []).forEach(function (detail) {
        if (!detail || typeof detail !== "object") {
          return;
        }
        var copy = Object.assign({}, detail);
        clearRouteAPIKey(copy);
        normalizeUsageSession(copy);
        if (routeKey && !copy.api_key && !copy.apiKey && !copy.APIKey && looksLikeUUIDSessionID(copy.session_id || copy.sessionId || copy.SessionID)) {
          copy.session_id = "codex";
        }
        if (routeKey && !copy.api_key && !copy.apiKey && !copy.APIKey) {
          copy.session_id = copy.session_id || copy.sessionId || copy.SessionID || "";
        }
        targetModel.details.push(copy);
      });
      target.models[modelName] = targetModel;
    });
  }

  function sanitizeUsagePayload(payload) {
    if (!payload || typeof payload !== "object" || !payload.usage || typeof payload.usage !== "object") {
      return payload;
    }
    var apis = payload.usage.apis;
    if (!apis || typeof apis !== "object") {
      return payload;
    }

    var copy = JSON.parse(JSON.stringify(payload));
    var sanitizedApis = {};
    Object.keys(copy.usage.apis || {}).forEach(function (apiName) {
      var sourceAPI = copy.usage.apis[apiName];
      if (!sourceAPI || typeof sourceAPI !== "object") {
        return;
      }
      var routeKey = looksLikeRouteIdentifier(apiName);
      var targetName = routeKey ? "" : apiName;
      if (!sanitizedApis[targetName]) {
        sanitizedApis[targetName] = { total_requests: 0, total_tokens: 0, models: {} };
      }
      mergeUsageAPI(sanitizedApis[targetName], sourceAPI, routeKey);
    });
    copy.usage.apis = sanitizedApis;
    return copy;
  }

  function isUsageStatisticsRequest(input) {
    var raw = typeof input === "string" ? input : input && input.url;
    if (!raw) {
      return false;
    }
    try {
      return new URL(raw, window.location.origin).pathname === "/v0/management/usage";
    } catch (_) {
      return false;
    }
  }

  function patchUsageStatisticsFetch() {
    if (window.__cliproxyUsageFetchPatched || typeof window.fetch !== "function") {
      return;
    }
    var originalFetch = window.fetch.bind(window);
    window.fetch = function (input, init) {
      return originalFetch(input, init).then(function (response) {
        if (!isUsageStatisticsRequest(input)) {
          return response;
        }
        return response
          .clone()
          .json()
          .then(function (payload) {
            return new Response(JSON.stringify(sanitizeUsagePayload(payload)), {
              status: response.status,
              statusText: response.statusText,
              headers: response.headers,
            });
          })
          .catch(function () {
            return response;
          });
      });
    };
    window.__cliproxyUsageFetchPatched = true;
  }

  function normalizedText(el) {
    return (el && el.textContent || "").replace(/\s+/g, " ").trim();
  }

  function isRequestEventsRoute() {
    try {
      if (window.sessionStorage.getItem(REQUEST_EVENTS_BOOT_FLAG) === "1") {
        return true;
      }
    } catch (_) {
      // Ignore storage errors.
    }
    return window.location.hash === REQUEST_EVENTS_HASH || requestEventsMode;
  }

  function clearRequestEventsRouteFlag() {
    requestEventsMode = false;
    try {
      window.sessionStorage.removeItem(REQUEST_EVENTS_BOOT_FLAG);
    } catch (_) {
      // Ignore storage errors.
    }
  }

  function setRequestEventsRoute(active) {
    requestEventsMode = !!active;
    if (active) {
      if (window.location.hash !== USAGE_HASH) {
        window.history.pushState(null, "", USAGE_HASH);
        window.dispatchEvent(new HashChangeEvent("hashchange"));
      }
      scheduleAdjust();
      return;
    }

    clearRequestEventsRouteFlag();
    if (window.location.hash !== USAGE_HASH) {
      window.history.pushState(null, "", USAGE_HASH);
    }
    scheduleAdjust();
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
    var expectedTitle = isRequestEventsRoute() ? REQUEST_EVENTS_TITLE : USAGE_PAGE_TITLE;
    var headings = document.querySelectorAll("h1");
    for (var i = 0; i < headings.length; i++) {
      var title = normalizedText(headings[i]);
      if (title === expectedTitle || title === USAGE_PAGE_TITLE || title === REQUEST_EVENTS_TITLE) {
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

  function decorateRequestEventsNavigationLink(link) {
    if (!link || link.dataset.cliproxyRequestEventsIcon === "1") {
      return;
    }
    link.innerHTML = REQUEST_EVENTS_ICON_SVG + '<span class="cliproxy-request-events-nav-label">' + REQUEST_EVENTS_TITLE + "</span>";
    link.dataset.cliproxyRequestEventsIcon = "1";
  }

  function ensureRequestEventsNavigation() {
    var usageLink = document.querySelector('a[href="' + USAGE_HASH + '"]');
    if (!usageLink || !usageLink.parentElement) {
      return;
    }
    if (!usageLink.dataset.cliproxyUsageRoutePatched) {
      usageLink.dataset.cliproxyUsageRoutePatched = "1";
      usageLink.addEventListener("click", function (event) {
        if (!isRequestEventsRoute()) {
          clearRequestEventsRouteFlag();
          return;
        }
        event.preventDefault();
        setRequestEventsRoute(false);
      });
    }

    var requestEventsLink = document.querySelector('a[href="' + REQUEST_EVENTS_HASH + '"]');
    if (!requestEventsLink) {
      requestEventsLink = usageLink.cloneNode(true);
      requestEventsLink.href = REQUEST_EVENTS_HASH;
      requestEventsLink.setAttribute("href", REQUEST_EVENTS_HASH);
      requestEventsLink.addEventListener("click", function (event) {
        event.preventDefault();
        setRequestEventsRoute(true);
      });
      usageLink.parentElement.insertBefore(requestEventsLink, usageLink.nextSibling);
    }
    decorateRequestEventsNavigationLink(requestEventsLink);

    var requestMode = isRequestEventsRoute();
    usageLink.classList.toggle("active", !requestMode && window.location.hash === USAGE_HASH);
    requestEventsLink.classList.toggle("active", requestMode);
  }

  function closestDirectChild(node, container) {
    while (node && node.parentElement && node.parentElement !== container) {
      node = node.parentElement;
    }
    return node && node.parentElement === container ? node : null;
  }

  function findDirectUsageChild(label) {
    var container = findUsageContainer();
    if (!container) {
      return null;
    }

    if (label === REQUEST_EVENTS_TITLE) {
      var candidates = document.querySelectorAll("h2,h3,h4,h5,h6,div,span");
      for (var i = 0; i < candidates.length; i++) {
        if (normalizedText(candidates[i]) !== label) {
          continue;
        }
        var card = candidates[i].closest(".card");
        var directCard = card && closestDirectChild(card, container);
        if (directCard) {
          return directCard;
        }
      }
    }

    var labelEl = findLabel(label);
    if (!labelEl) {
      return null;
    }

    return closestDirectChild(labelEl, container);
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

  function setNodeVisible(node, visible) {
    if (!node) {
      return;
    }
    if (visible) {
      if (node.dataset && node.dataset.cliproxyPreviousDisplay !== undefined) {
        node.style.display = node.dataset.cliproxyPreviousDisplay;
        delete node.dataset.cliproxyPreviousDisplay;
      } else if (node.style.display === "none") {
        node.style.display = "";
      }
      return;
    }
    if (node.dataset && node.dataset.cliproxyPreviousDisplay === undefined) {
      node.dataset.cliproxyPreviousDisplay = node.style.display || "";
    }
    node.style.display = "none";
  }

  function directUsageChildHasLabel(child, labels) {
    var text = normalizedText(child);
    for (var i = 0; i < labels.length; i++) {
      if (text.indexOf(labels[i]) === 0 || text.indexOf(labels[i]) !== -1) {
        return true;
      }
    }
    return false;
  }

  function removeRequestEventsThresholdFilters(requestEvents) {
    if (!requestEvents) {
      return;
    }
    var filters = requestEvents.querySelectorAll('[class*="UsagePage-module__requestEventsFilterItem"]');
    for (var i = 0; i < filters.length; i++) {
      var label = filters[i].querySelector('[class*="UsagePage-module__requestEventsFilterLabel"]');
      var text = normalizedText(label || filters[i]);
      if (text === "Fresh tokens >" || text === "Total tokens >") {
        filters[i].remove();
      }
    }
  }

  function findRequestEventsCardHeader(requestEvents) {
    if (!requestEvents) {
      return null;
    }
    var title = null;
    var candidates = requestEvents.querySelectorAll("h1,h2,h3,h4,h5,h6,div,span");
    for (var i = 0; i < candidates.length; i++) {
      if (normalizedText(candidates[i]) === REQUEST_EVENTS_TITLE) {
        title = candidates[i];
        break;
      }
    }
    return title ? title.parentElement : requestEvents.firstElementChild;
  }

  function ensureRequestEventsTopRow(requestEvents) {
    var requestHeader = findRequestEventsCardHeader(requestEvents);
    if (!requestHeader) {
      return null;
    }

    var topRow = document.getElementById(REQUEST_EVENTS_TOP_ROW_ID);
    if (!topRow) {
      topRow = document.createElement("div");
      topRow.id = REQUEST_EVENTS_TOP_ROW_ID;
    }

    if (topRow.parentElement !== requestHeader) {
      requestHeader.appendChild(topRow);
    }

    var requestActions = requestHeader.querySelector('[class*="UsagePage-module__requestEventsActions"]');
    if (requestActions && requestActions.parentElement !== topRow) {
      topRow.appendChild(requestActions);
    }

    return topRow;
  }

  function moveHeaderControlsToRequestEvents(header, requestEvents) {
    if (!header || !requestEvents) {
      return;
    }
    var headerActions = header.querySelector('[class*="UsagePage-module__headerActions"]');
    if (!headerActions) {
      headerActions = document.getElementById(REQUEST_EVENTS_HEADER_CONTROLS_ID);
    }
    if (!headerActions) {
      return;
    }

    headerActions.id = REQUEST_EVENTS_HEADER_CONTROLS_ID;
    headerActions.dataset.cliproxyMovedToRequestEvents = "1";

    var topRow = ensureRequestEventsTopRow(requestEvents);
    if (!topRow) {
      requestEvents.insertBefore(headerActions, requestEvents.firstChild);
      return;
    }

    if (headerActions.parentElement !== topRow) {
      topRow.appendChild(headerActions);
    }
  }

  function moveTokenSummaryToRequestEventsToolbar(requestEvents) {
    if (!requestEvents) {
      return;
    }

    var toolbar = requestEvents.querySelector('[class*="UsagePage-module__requestEventsToolbar"]');
    var tokenSummary = requestEvents.querySelector('[class*="UsagePage-module__requestEventsTokenSummary"]');
    if (!toolbar || !tokenSummary || tokenSummary.parentElement === toolbar) {
      return;
    }

    toolbar.appendChild(tokenSummary);
  }

  function formatRequestEventsBlock(requestEvents) {
    if (!requestEvents) {
      return;
    }
    requestEvents.id = REQUEST_EVENTS_CARD_ID;
    ensureRequestEventsTopRow(requestEvents);
    moveTokenSummaryToRequestEventsToolbar(requestEvents);
  }

  function setRequestEventsPage(index) {
    requestEventsPageIndex = Math.max(0, index);
    scheduleAdjust();
  }

  function createRequestEventsPager() {
    var pager = document.createElement("div");
    pager.id = REQUEST_EVENTS_PAGER_ID;

    var status = document.createElement("span");
    status.className = "cliproxy-request-events-pager-status";

    var actions = document.createElement("div");
    actions.className = "cliproxy-request-events-pager-actions";

    var previous = document.createElement("button");
    previous.type = "button";
    previous.className = "btn btn-secondary btn-sm";
    previous.textContent = "Previous 100";
    previous.addEventListener("click", function () {
      setRequestEventsPage(requestEventsPageIndex - 1);
    });

    var next = document.createElement("button");
    next.type = "button";
    next.className = "btn btn-secondary btn-sm";
    next.textContent = "Next 100";
    next.addEventListener("click", function () {
      setRequestEventsPage(requestEventsPageIndex + 1);
    });

    actions.appendChild(previous);
    actions.appendChild(next);
    pager.appendChild(status);
    pager.appendChild(actions);
    return pager;
  }

  function paginateRequestEventsTable(requestEvents) {
    if (!requestEvents || !isRequestEventsRoute()) {
      return;
    }

    var tableWrapper = requestEvents.querySelector('[class*="UsagePage-module__requestEventsTableWrapper"]');
    var tbody = tableWrapper && tableWrapper.querySelector("tbody");
    if (!tableWrapper || !tbody) {
      return;
    }

    var rows = Array.prototype.slice.call(tbody.querySelectorAll("tr"));
    if (rows.length === 0) {
      return;
    }

    var signature = rows.length + ":" + normalizedText(rows[0]).slice(0, 120);
    if (signature !== requestEventsRowsSignature) {
      requestEventsRowsSignature = signature;
      requestEventsPageIndex = 0;
    }

    var pageCount = Math.max(1, Math.ceil(rows.length / REQUEST_EVENTS_PAGE_SIZE));
    if (requestEventsPageIndex >= pageCount) {
      requestEventsPageIndex = pageCount - 1;
    }

    var start = requestEventsPageIndex * REQUEST_EVENTS_PAGE_SIZE;
    var end = Math.min(rows.length, start + REQUEST_EVENTS_PAGE_SIZE);
    rows.forEach(function (row, index) {
      row.style.display = index >= start && index < end ? "" : "none";
    });

    var pager = document.getElementById(REQUEST_EVENTS_PAGER_ID);
    if (!pager) {
      pager = createRequestEventsPager();
    }
    if (pager.parentElement !== requestEvents) {
      var meta = requestEvents.querySelector('[class*="UsagePage-module__requestEventsMeta"]');
      requestEvents.insertBefore(pager, meta ? meta.nextElementSibling : tableWrapper);
    }

    var status = pager.querySelector(".cliproxy-request-events-pager-status");
    var previous = pager.querySelector("button:first-of-type");
    var next = pager.querySelector("button:last-of-type");
    if (status) {
      status.textContent = "Showing " + (start + 1).toLocaleString() + "-" + end.toLocaleString() + " of " + rows.length.toLocaleString() + " events";
    }
    if (previous) {
      previous.disabled = requestEventsPageIndex === 0;
    }
    if (next) {
      next.disabled = requestEventsPageIndex >= pageCount - 1;
    }
  }

  function normalizeRequestEventsLogName(value) {
    value = String(value || "").trim().replace(/^"+|"+$/g, "");
    if (!value) {
      return "";
    }
    value = value
      .replace(/^\/home\/gismar\/\.cli-proxy-api\/logs\/?/i, "")
      .replace(/^\\\\wsl\.localhost\\Ubuntu-24\.04\\home\\gismar\\\.cli-proxy-api\\logs\\?/i, "")
      .replace(/^logs[\\/]/i, "")
      .replace(/[\\/]+/g, "\\");
    return value.replace(/^\\+/, "");
  }

  function buildRequestEventsLogPath(logName) {
    logName = normalizeRequestEventsLogName(logName);
    return logName ? REQUEST_EVENTS_LOG_ROOT + "\\" + logName : "";
  }

  function showRequestEventsCopyFallback(path) {
    var requestEvents = findDirectUsageChild(REQUEST_EVENTS_TITLE);
    if (!requestEvents) {
      window.prompt("Copy log path", path);
      return;
    }

    var fallback = document.getElementById(REQUEST_EVENTS_COPY_FALLBACK_ID);
    if (!fallback) {
      fallback = document.createElement("div");
      fallback.id = REQUEST_EVENTS_COPY_FALLBACK_ID;
      fallback.innerHTML =
        '<span>Log path</span><input type="text" readonly aria-label="Log path" />';
    }

    var input = fallback.querySelector("input");
    if (input) {
      input.value = path;
    }

    var tableWrapper = requestEvents.querySelector('[class*="UsagePage-module__requestEventsTableWrapper"]');
    if (tableWrapper && fallback.parentElement !== requestEvents) {
      requestEvents.insertBefore(fallback, tableWrapper);
    }
    if (input) {
      input.focus();
      input.select();
    }
  }

  function copyText(text) {
    if (navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
      return navigator.clipboard.writeText(text).then(
        function () {
          return true;
        },
        function () {
          return false;
        }
      );
    }

    return new Promise(function (resolve) {
      var input = document.createElement("textarea");
      input.value = text;
      input.setAttribute("readonly", "");
      input.style.position = "fixed";
      input.style.left = "-9999px";
      input.style.top = "0";
      document.body.appendChild(input);
      input.focus();
      input.select();
      try {
        resolve(document.execCommand("copy"));
      } catch (_) {
        resolve(false);
      } finally {
        input.remove();
      }
    });
  }

  function patchRequestEventsLogCopy() {
    if (window.__cliproxyRequestEventsLogCopyPatched) {
      return;
    }
    document.addEventListener(
      "click",
      function (event) {
        if (!isRequestEventsRoute()) {
          return;
        }
        var button = event.target && event.target.closest ? event.target.closest("button") : null;
        if (!button || normalizedText(button).toLowerCase() !== "copy") {
          return;
        }
        var fullPath = buildRequestEventsLogPath(button.getAttribute("title") || "");
        if (!fullPath) {
          return;
        }

        event.preventDefault();
        event.stopPropagation();
        if (typeof event.stopImmediatePropagation === "function") {
          event.stopImmediatePropagation();
        }

        copyText(fullPath).then(function (copied) {
          if (!copied) {
            showRequestEventsCopyFallback(fullPath);
          }
        });
      },
      true
    );
    window.__cliproxyRequestEventsLogCopyPatched = true;
  }

  function scheduleUsageChartReflow() {
    window.setTimeout(function () {
      window.dispatchEvent(new Event("resize"));
      if (window.Chart && window.Chart.instances) {
        try {
          Object.keys(window.Chart.instances).forEach(function (key) {
            var chart = window.Chart.instances[key];
            if (chart && typeof chart.update === "function") {
              chart.update();
            }
          });
        } catch (_) {
          // Resize dispatch above still covers the normal Chart.js path.
        }
      }
    }, 60);
  }

  function findHeaderRefreshButton() {
    var buttons = document.querySelectorAll("button");
    for (var i = 0; i < buttons.length; i++) {
      var text = normalizedText(buttons[i]);
      if (text === "Refresh" || text === "刷新") {
        return buttons[i];
      }
    }
    return null;
  }

  function ensureChartLineControls() {
    if (isRequestEventsRoute()) {
      return;
    }
    var chartCard = findCard(CHART_LINES_TITLE);
    if (!chartCard) {
      return;
    }

    var header =
      chartCard.querySelector(".card-header") ||
      chartCard.querySelector('[class*="card-header"]') ||
      chartCard.firstElementChild;
    if (header) {
      var extra =
        header.querySelector('[class*="UsagePage-module__chartLineHeader"]') ||
        header.lastElementChild;
      if (extra && !document.getElementById(CHART_LINES_REFRESH_ID)) {
        var refresh = document.createElement("button");
        refresh.id = CHART_LINES_REFRESH_ID;
        refresh.type = "button";
        refresh.className = "btn btn-secondary btn-sm";
        refresh.textContent = "Refresh";
        refresh.addEventListener("click", function () {
          var headerRefresh = findHeaderRefreshButton();
          if (headerRefresh) {
            headerRefresh.click();
          }
          scheduleUsageChartReflow();
        });
        extra.appendChild(refresh);
      }
    }

    if (chartCard.dataset[CHART_LINES_OBSERVER_FLAG] === "1") {
      return;
    }
    chartCard.dataset[CHART_LINES_OBSERVER_FLAG] = "1";
    var observer = new MutationObserver(function () {
      scheduleUsageChartReflow();
    });
    observer.observe(chartCard, {
      childList: true,
      subtree: true,
      characterData: true,
      attributes: true,
      attributeFilter: ["aria-expanded", "title"],
    });
  }

  function updateUsagePageTitle(header, title) {
    if (!header) {
      return;
    }
    var pageTitle = header.querySelector("h1");
    if (pageTitle && normalizedText(pageTitle) !== title) {
      pageTitle.textContent = title;
    }
  }

  function applyUsagePageMode() {
    var container = findUsageContainer();
    if (!container) {
      ensureRequestEventsNavigation();
      return;
    }

    var requestMode = isRequestEventsRoute();
    var header = container.querySelector('[class*="UsagePage-module__header"]');
    var requestEvents = findDirectUsageChild(REQUEST_EVENTS_TITLE);
    var noisyLabels = [
      "API Details",
      "Model Statistics",
      "Credential Statistics",
    ];

    ensureRequestEventsNavigation();
    ensureRequestEventsStyles();
    patchRequestEventsLogCopy();
    ensureChartLineControls();
    document.documentElement.classList.toggle("cliproxy-request-events-route", requestMode);
    if (requestMode && window.location.hash !== REQUEST_EVENTS_HASH) {
      window.history.replaceState(null, "", REQUEST_EVENTS_HASH);
    }
    if (requestMode) {
      requestEventsMode = true;
      try {
        window.sessionStorage.removeItem(REQUEST_EVENTS_BOOT_FLAG);
      } catch (_) {
        // Ignore storage errors.
      }
    }
    updateUsagePageTitle(header, requestMode ? REQUEST_EVENTS_TITLE : USAGE_PAGE_TITLE);
    removeRequestEventsThresholdFilters(requestEvents);
    formatRequestEventsBlock(requestEvents);
    paginateRequestEventsTable(requestEvents);

    if (header && requestEvents) {
      if (header.nextElementSibling !== requestEvents) {
        container.insertBefore(requestEvents, header.nextElementSibling);
      }
      moveHeaderControlsToRequestEvents(header, requestEvents);
    }

    var headerControls = document.getElementById(REQUEST_EVENTS_HEADER_CONTROLS_ID);
    setNodeVisible(headerControls, requestMode);

    var children = Array.prototype.slice.call(container.children);
    for (var i = 0; i < children.length; i++) {
      var child = children[i];
      if (child === header) {
        setNodeVisible(child, true);
        continue;
      }
      if (child === requestEvents) {
        setNodeVisible(child, requestMode);
        continue;
      }
      if (directUsageChildHasLabel(child, noisyLabels)) {
        setNodeVisible(child, false);
        continue;
      }
      setNodeVisible(child, !requestMode);
    }
  }

  function ensureRequestEventsStyles() {
    if (document.getElementById(REQUEST_EVENTS_STYLE_ID)) {
      return;
    }
    var style = document.createElement("style");
    style.id = REQUEST_EVENTS_STYLE_ID;
    style.textContent =
      '.cliproxy-request-events-nav-icon{' +
      "width:18px;height:18px;flex:0 0 18px;margin-right:10px;color:currentColor" +
      "}" +
      '.cliproxy-request-events-nav-label{' +
      "min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" +
      "}" +
      'a[href="' + REQUEST_EVENTS_HASH + '"]{' +
      "display:flex!important;align-items:center!important" +
      "}" +
      "#" + REQUEST_EVENTS_CARD_ID + "{" +
      "min-height:0" +
      "}" +
      ".cliproxy-request-events-route #" + REQUEST_EVENTS_CARD_ID + "{" +
      "display:flex!important;flex-direction:column!important;height:calc(100vh - 168px)!important;" +
      "min-height:520px!important;overflow:hidden!important" +
      "}" +
      ".cliproxy-request-events-route #" + REQUEST_EVENTS_CARD_ID + " .card-header{" +
      "display:grid!important;grid-template-columns:minmax(180px,auto) minmax(0,1fr)!important;" +
      "align-items:center!important;gap:12px!important;flex:0 0 auto!important" +
      "}" +
      "#" + REQUEST_EVENTS_TOP_ROW_ID + "{" +
      "display:flex!important;align-items:center!important;justify-content:flex-end!important;" +
      "gap:10px!important;flex-wrap:nowrap!important;min-width:0!important;overflow-x:auto!important;" +
      "padding-bottom:2px!important" +
      "}" +
      "#" + REQUEST_EVENTS_TOP_ROW_ID + ' [class*="UsagePage-module__requestEventsActions"]{' +
      "display:flex!important;align-items:center!important;gap:8px!important;flex-wrap:nowrap!important;flex:0 0 auto!important" +
      "}" +
      '[class*="UsagePage-module__requestEventsToolbar"]{' +
      "display:grid!important;grid-template-columns:repeat(4,minmax(120px,180px)) minmax(420px,1fr)!important;" +
      "align-items:end!important;justify-content:start!important;gap:8px!important" +
      "}" +
      "#" + REQUEST_EVENTS_HEADER_CONTROLS_ID + "{" +
      "display:flex!important;align-items:center!important;justify-content:flex-end!important;" +
      "gap:8px!important;flex-wrap:nowrap!important;margin-left:0!important;min-width:0!important;flex:0 0 auto!important" +
      "}" +
      "#" + REQUEST_EVENTS_HEADER_CONTROLS_ID + ' [class*="UsagePage-module__timeRangeGroup"]{' +
      "margin-right:4px!important" +
      "}" +
      '[class*="UsagePage-module__requestEventsFilterItem"]{' +
      "min-width:0!important;width:100%!important" +
      "}" +
      '[class*="UsagePage-module__requestEventsSelect"]{' +
      "min-width:0!important;width:100%!important" +
      "}" +
      '[class*="UsagePage-module__requestEventsTokenSummary"]{' +
      "grid-column:auto!important;justify-self:end!important;margin-left:0!important;" +
      "min-width:0!important;width:auto!important;flex:0 0 auto!important;white-space:nowrap!important;" +
      "align-items:flex-end!important" +
      "}" +
      '[class*="UsagePage-module__requestEventsTokenSummaryGrid"]{' +
      "grid-template-columns:repeat(4,minmax(72px,max-content))!important" +
      "}" +
      "#" + REQUEST_EVENTS_COPY_FALLBACK_ID + "{" +
      "display:grid!important;grid-template-columns:auto minmax(0,1fr)!important;align-items:center!important;" +
      "gap:8px!important;margin:0 0 8px!important;padding:8px 10px!important;border:1px solid var(--border-color)!important;" +
      "border-radius:8px!important;background:var(--bg-secondary)!important;color:var(--text-secondary)!important;" +
      "font-size:12px!important;max-width:100%!important;box-sizing:border-box!important" +
      "}" +
      "#" + REQUEST_EVENTS_COPY_FALLBACK_ID + " input{" +
      "width:100%!important;min-width:0!important;box-sizing:border-box!important;border:1px solid var(--border-color)!important;" +
      "border-radius:6px!important;background:var(--bg-primary)!important;color:var(--text-primary)!important;" +
      "padding:6px 8px!important;font:inherit!important" +
      "}" +
      ".cliproxy-request-events-route #" + REQUEST_EVENTS_CARD_ID + " th:last-child," +
      ".cliproxy-request-events-route #" + REQUEST_EVENTS_CARD_ID + " td:last-child{" +
      "width:64px!important;min-width:64px!important;max-width:64px!important;text-align:center!important;" +
      "white-space:nowrap!important;padding-left:6px!important;padding-right:6px!important" +
      "}" +
      ".cliproxy-request-events-route #" + REQUEST_EVENTS_CARD_ID + " td:last-child button{" +
      "width:48px!important;padding-left:0!important;padding-right:0!important" +
      "}" +
      "#" + REQUEST_EVENTS_PAGER_ID + "{" +
      "display:flex!important;align-items:center!important;justify-content:space-between!important;" +
      "gap:12px!important;flex:0 0 auto!important;padding:10px 0 8px!important;" +
      "color:var(--text-secondary)!important;font-size:13px!important" +
      "}" +
      "#" + REQUEST_EVENTS_PAGER_ID + " .cliproxy-request-events-pager-actions{" +
      "display:flex!important;align-items:center!important;gap:8px!important;flex-wrap:wrap!important" +
      "}" +
      "#" + REQUEST_EVENTS_PAGER_ID + " button:disabled{" +
      "opacity:.45!important;cursor:not-allowed!important" +
      "}" +
      ".cliproxy-request-events-route #" + REQUEST_EVENTS_CARD_ID + ' [class*="UsagePage-module__requestEventsTableWrapper"]{' +
      "flex:1 1 auto!important;min-height:0!important;height:auto!important;max-height:none!important;overflow:auto!important" +
      "}" +
      "@media(max-width:1100px){" +
      '[class*="UsagePage-module__requestEventsToolbar"]{' +
      "grid-template-columns:repeat(2,minmax(150px,1fr))!important" +
      "}" +
      '[class*="UsagePage-module__requestEventsTokenSummary"]{' +
      "grid-column:1/-1!important" +
      "}" +
      ".cliproxy-request-events-route #" + REQUEST_EVENTS_CARD_ID + " .card-header{" +
      "grid-template-columns:1fr!important;align-items:flex-start!important" +
      "}" +
      "#" + REQUEST_EVENTS_TOP_ROW_ID + "{" +
      "justify-content:flex-start!important;width:100%!important" +
      "}" +
      "#" + REQUEST_EVENTS_HEADER_CONTROLS_ID + "{" +
      "justify-content:flex-start!important;width:100%!important;margin-left:0!important" +
      "}" +
      "}" +
      "@media(max-width:768px){" +
      '[class*="UsagePage-module__requestEventsToolbar"]{' +
      "grid-template-columns:repeat(2,minmax(0,1fr))!important" +
      "}" +
      '[class*="UsagePage-module__requestEventsTokenSummary"]{' +
      "justify-self:stretch!important;align-items:flex-start!important;width:100%!important" +
      "}" +
      "}" +
      "@media(max-width:520px){" +
      '[class*="UsagePage-module__requestEventsToolbar"]{' +
      "grid-template-columns:1fr!important" +
      "}" +
      "}";
    document.head.appendChild(style);
  }

  function adjustUsageDetails() {
    if (window.location.hash !== REQUEST_EVENTS_HASH && window.location.hash !== USAGE_HASH) {
      clearRequestEventsRouteFlag();
      document.documentElement.classList.remove("cliproxy-request-events-route");
    }
    if (window.location.hash !== USAGE_HASH && window.location.hash !== REQUEST_EVENTS_HASH) {
      ensureRequestEventsNavigation();
      return;
    }

    var automaticCalibration = findCard(AUTO_CALIBRATION_TITLE);

    if (automaticCalibration) {
      automaticCalibration.remove();
    }

    applyUsagePageMode();
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
  patchUsageStatisticsFetch();

  var observer = new MutationObserver(scheduleAdjust);
  observer.observe(document.documentElement, { childList: true, subtree: true });

  scheduleAdjust();
})();
