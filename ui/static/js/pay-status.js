(function () {
  var script = document.currentScript;
  var paymentId = script ? script.getAttribute("data-payment-id") : null;
  if (!paymentId) return;

  var pill = document.getElementById("pay-status-pill");
  var message = document.getElementById("pay-confirm-message");
  if (!pill) return;

  function render(status) {
    if (status === "PAID") {
      pill.textContent = "Payment confirmed";
      pill.setAttribute("data-status", "PAID");
      if (message) message.textContent = "Payment confirmed \uD83C\uDF89";
      // Give the page a moment to show the confirmed state, then load the course.
      setTimeout(function () {
        window.location.reload();
      }, 800);
    }
  }

  function poll() {
    fetch("/student/pay/" + paymentId + "/status", {
      headers: { "Accept": "application/json" },
      credentials: "same-origin"
    })
      .then(function (res) { return res.json(); })
      .then(function (data) { render(data.status); })
      .catch(function () { /* transient; retry next tick */ });
  }

  poll();
  setInterval(poll, 3000);
})();
