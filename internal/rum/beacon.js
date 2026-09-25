/* Hosbah RUM beacon (MH-575 H5). Clean-room, no dependencies. <script defer src="…/rum.js" data-key="hbr_…"></script> */
(function () {
  "use strict";
  var script = document.currentScript;
  var key = script && script.getAttribute("data-key");
  if (!key) return;
  var endpoint = script.src.replace(/\/rum\.js(\?.*)?$/, "/api/rum/" + key);

  var m = { lcp: null, cls: 0, inp: null, fcp: null, ttfb: null };

  function observe(type, cb) {
    try {
      new PerformanceObserver(function (list) { cb(list.getEntries()); }).observe({ type: type, buffered: true });
    } catch (e) { /* type unsupported in this browser */ }
  }

  observe("largest-contentful-paint", function (entries) {
    var last = entries[entries.length - 1];
    if (last) m.lcp = last.renderTime || last.loadTime;
  });
  observe("layout-shift", function (entries) {
    for (var i = 0; i < entries.length; i++) {
      if (!entries[i].hadRecentInput) m.cls += entries[i].value;
    }
  });
  observe("paint", function (entries) {
    for (var i = 0; i < entries.length; i++) {
      if (entries[i].name === "first-contentful-paint") m.fcp = entries[i].startTime;
    }
  });
  // INP: the slowest interaction's duration seen so far. Falls back to first-input's delay (FID)
  // on browsers that support Event Timing but not the "durationThreshold" INP shape (older Safari).
  var haveEventTiming = false;
  observe("event", function (entries) {
    haveEventTiming = true;
    for (var i = 0; i < entries.length; i++) {
      var d = entries[i].duration;
      if (m.inp === null || d > m.inp) m.inp = d;
    }
  });
  observe("first-input", function (entries) {
    if (haveEventTiming || !entries[0]) return;
    m.inp = entries[0].processingStart - entries[0].startTime;
  });
  try {
    var nav = performance.getEntriesByType("navigation")[0];
    if (nav) m.ttfb = nav.responseStart;
  } catch (e) { /* Navigation Timing L2 unsupported */ }

  var sent = false;
  function send() {
    if (sent) return;
    sent = true;
    var conn = navigator.connection || {};
    var isMobile = false;
    try { isMobile = matchMedia("(max-width: 768px)").matches; } catch (e) {}
    var body = JSON.stringify({
      path: location.pathname,
      device: isMobile ? "mobile" : "desktop",
      effective_type: conn.effectiveType || "",
      lcp_ms: m.lcp, cls: m.cls, inp_ms: m.inp, fcp_ms: m.fcp, ttfb_ms: m.ttfb
    });
    if (navigator.sendBeacon) {
      navigator.sendBeacon(endpoint, new Blob([body], { type: "application/json" }));
    } else {
      fetch(endpoint, { method: "POST", body: body, headers: { "Content-Type": "application/json" }, keepalive: true });
    }
  }
  document.addEventListener("visibilitychange", function () {
    if (document.visibilityState === "hidden") send();
  });
  addEventListener("pagehide", send);
})();
