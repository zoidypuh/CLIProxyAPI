(function () {
  "use strict";

  var USAGE_HASH = "#/usage";
  var AUTO_CALIBRATION_TITLE = "Automatic Calibration";

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

  function adjustUsageDetails() {
    if (window.location.hash !== USAGE_HASH) {
      return;
    }

    var automaticCalibration = findCard(AUTO_CALIBRATION_TITLE);

    if (automaticCalibration) {
      automaticCalibration.remove();
    }
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
