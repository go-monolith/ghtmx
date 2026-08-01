(function() {
  let ghtmx_reloadSrc = window.ghtmx_reloadSrc || new EventSource("/_ghtmx/reload/events");
  ghtmx_reloadSrc.onmessage = (event) => {
    if (event && event.data === "reload") {
      window.location.reload();
    }
  };
  window.ghtmx_reloadSrc = ghtmx_reloadSrc;
  window.onbeforeunload = () => window.ghtmx_reloadSrc.close();
})();
