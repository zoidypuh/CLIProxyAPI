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

  var scheduled = false;
  function scheduleAdjust() {
    if (scheduled) {
      return;
    }
    scheduled = true;
    window.requestAnimationFrame(function () {
      scheduled = false;
      adjustUsageDetails();
    });
  }

  window.addEventListener("hashchange", scheduleAdjust);
  window.addEventListener("load", scheduleAdjust);

  var observer = new MutationObserver(scheduleAdjust);
  observer.observe(document.documentElement, { childList: true, subtree: true });

  scheduleAdjust();
})();
