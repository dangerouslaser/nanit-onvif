(function() {
  "use strict";

  function formatTime(ts) {
    var d = new Date(ts * 1000);
    var h = d.getHours();
    var m = d.getMinutes();
    var ampm = h >= 12 ? "PM" : "AM";
    h = h % 12 || 12;
    return h + ":" + (m < 10 ? "0" : "") + m + " " + ampm;
  }

  // Copy RTSP URL to clipboard (fallback for non-HTTPS contexts)
  function copyToClipboard(text, btn) {
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(function() {
        btn.textContent = "Copied!";
        setTimeout(function() { btn.textContent = "Copy"; }, 1500);
      });
    } else {
      var ta = document.createElement("textarea");
      ta.value = text;
      ta.style.position = "fixed";
      ta.style.left = "-9999px";
      document.body.appendChild(ta);
      ta.select();
      document.execCommand("copy");
      document.body.removeChild(ta);
      btn.textContent = "Copied!";
      setTimeout(function() { btn.textContent = "Copy"; }, 1500);
    }
  }

  document.addEventListener("click", function(e) {
    if (e.target.classList.contains("btn-copy")) {
      copyToClipboard(e.target.getAttribute("data-copy"), e.target);
    }
  });

  // SSE for real-time state updates
  if (document.querySelector(".baby-grid")) {
    var evtSource = new EventSource("/api/events");

    evtSource.onmessage = function(event) {
      var data = JSON.parse(event.data);
      var card = document.querySelector('[data-baby-uid="' + data.baby_uid + '"]');
      if (!card) return;

      // Update stream state
      if (data.stream_state !== undefined) {
        var dot = card.querySelector(".stream-dot");
        var label = card.querySelector(".stream-label");
        if (dot && label) {
          dot.className = "status-dot stream-dot " + data.stream_state.toLowerCase();
          label.textContent = data.stream_state;
        }
      }

      // Update websocket status
      if (data.websocket_alive !== undefined) {
        var wsLabel = card.querySelector(".ws-label");
        if (wsLabel) {
          wsLabel.textContent = data.websocket_alive ? "Connected" : "Disconnected";
        }
      }

      // Update temperature
      if (data.temperature !== undefined) {
        var tempEl = card.querySelector(".temp-value");
        if (tempEl) tempEl.textContent = (data.temperature * 9 / 5 + 32).toFixed(1) + "\u00b0F";
      }

      // Update humidity
      if (data.humidity !== undefined) {
        var humEl = card.querySelector(".humidity-value");
        if (humEl) humEl.textContent = data.humidity.toFixed(1) + "%";
      }

      // Update night mode
      if (data.is_night !== undefined && data.is_night !== null) {
        var nightEl = card.querySelector(".night-value");
        if (nightEl) nightEl.textContent = data.is_night ? "Yes" : "No";
      }

      // Update last motion
      if (data.motion_timestamp && data.motion_timestamp > 0) {
        var motionEl = card.querySelector(".motion-value");
        if (motionEl) motionEl.textContent = formatTime(data.motion_timestamp);
      }

      // Update last sound
      if (data.sound_timestamp && data.sound_timestamp > 0) {
        var soundEl = card.querySelector(".sound-value");
        if (soundEl) soundEl.textContent = formatTime(data.sound_timestamp);
      }
    };

    evtSource.onerror = function() {
      // Reconnect handled automatically by EventSource
    };
  }
})();
