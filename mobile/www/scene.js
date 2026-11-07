// Draws the Sentinela scene (see scene.css) into every .scene element. The
// lighthouse paths come from the web app's hand-drawn mark (Lighthouse.tsx).
(function () {
  var lh =
    '<svg class="lh-mark" viewBox="0 0 48 48" x="92" y="8" width="50" height="50" overflow="visible">' +
    '<defs><linearGradient id="mb-s" gradientUnits="userSpaceOnUse" x1="24" y1="0" x2="50" y2="0">' +
    '<stop offset="0" stop-color="var(--lamp)" stop-opacity="0.85"/><stop offset="1" stop-color="var(--lamp)" stop-opacity="0"/></linearGradient></defs>' +
    '<path class="lh-beam" d="M24 13L50 6V22L24 15Z" fill="url(#mb-s)"/>' +
    '<path d="M19.6 19H28.4L30.8 40H17.2Z" class="f-paper"/>' +
    '<path d="M19.0 24.2H29.0L29.45 28.2H18.55ZM18.05 32.6H29.95L30.41 36.6H17.59Z" class="f-band"/>' +
    '<path d="M19.6 19Q23.3 19 28.4 19M19.6 19Q24.4 19 28.4 19M28.4 19Q29.7 30.6 30.8 40M28.4 19Q29.4 27.9 30.8 40M30.8 40Q24.3 40 17.2 40M30.8 40Q23 40 17.2 40M17.2 40Q18.4 30.3 19.6 19M17.2 40Q18.5 27.9 19.6 19" class="ink"/>' +
    '<path d="M22.7 40C22.7 39.5 22.3 37.6 22.5 36.8C22.7 36.1 23.6 35.5 24 35.5C24.5 35.4 25.2 36 25.4 36.7C25.6 37.5 25.3 39.5 25.3 40.1" class="ink thin"/>' +
    '<path d="M20.6 10.8H27.4V17.2H20.6Z" class="f-glass"/><circle cx="24" cy="14" r="2.2" class="f-lamp lh-lamp"/>' +
    '<path d="M20.6 10.8Q23.6 10.8 27.4 10.8M27.4 10.8Q27.4 14.2 27.4 17.2M27.4 17.2Q24.6 17.2 20.6 17.2M20.6 17.2Q20.6 14.3 20.6 10.8" class="ink thin"/>' +
    '<path d="M16.6 17.2H31.4L30.6 19.2H17.4Z" class="f-roof"/>' +
    '<path d="M16.6 17.2Q23.6 17.2 31.4 17.2M31.4 17.2Q31 18.2 30.6 19.2M30.6 19.2Q25.1 19.2 17.4 19.2M17.4 19.2Q17 18.2 16.6 17.2" class="ink thin"/>' +
    '<path d="M19.6 10.8Q20.6 6.4 24 5.8Q27.4 6.4 28.4 10.8Z" class="f-roof"/>' +
    '<path d="M19.6 10.8C19.9 10.2 20.3 8.1 21 7.3C21.8 6.4 23 5.7 24 5.7C25 5.7 26.2 6.4 26.9 7.3C27.6 8.1 28.2 10.1 28.4 10.7M24 5.8Q24 4.4 24 2.8" class="ink"/>' +
    '<path d="M12.5 42.6Q14.4 41.4 16.4 40.2M16.4 40.2Q23.8 40.2 31.6 40.2M31.6 40.2Q33.8 41.6 35.5 42.6" class="ink"/>' +
    '<path d="M2 44.1C2.7 44.5 4.9 46.2 6.4 46.2C7.8 46.2 9.4 44.2 10.8 44.2C12.3 44.2 13.7 46.2 15.1 46.2C16.6 46.2 18.1 44.2 19.6 44.2C21.1 44.2 22.6 46.4 24.1 46.4C25.6 46.4 26.9 44 28.4 44C29.9 44 31.4 46.2 32.9 46.3C34.3 46.3 35.8 44.1 37.3 44.1C38.7 44.2 40 46.3 41.5 46.3C43 46.3 45.3 44.6 46.1 44.2" class="ink thin lh-sea"/>' +
    "</svg>";
  function wave(y, amp, period) {
    var d = "M0 " + y;
    for (var x = 0, i = 0; x < 480; x += period / 2, i++) d += " q" + period / 4 + " " + (i % 2 ? amp : -amp) + " " + period / 2 + " 0";
    return d + " L480 120 L0 120 Z";
  }
  var ocean =
    '<svg class="ocean" viewBox="0 0 240 120" preserveAspectRatio="xMidYMax slice" aria-hidden="true">' +
    lh +
    '<path class="o-wave o-back" d="' + wave(46, 5, 60) + '"/>' +
    '<g transform="translate(34 0)"><g class="o-boat"><path d="M150 44 Q162 49 175 44 L171 50 Q162 52.5 154 50 Z"/>' +
    '<path d="M162.4 44.6 V28.5 L172 42.4 Z M161.6 31 L154.5 42.6 H161.6 Z"/></g></g>' +
    '<path class="o-wave o-mid" d="' + wave(62, 6, 80) + '"/>' +
    '<g class="o-fish"><path d="M60 86 Q66 81 73 85.5 Q66 90 60 86 Z M73 85.5 L77 82.5 L76.6 88.6 Z"/></g>' +
    '<path class="o-wave o-front" d="' + wave(80, 7, 120) + '"/></svg>';
  var gulls = '<svg class="gulls" viewBox="0 0 60 20" aria-hidden="true"><path d="M2 10q5-6 10 0q5-6 10 0"/><path d="M30 5q4-5 8 0q4-5 8 0"/></svg>';
  var stars = [[12, 14], [30, 8], [52, 18], [70, 10], [86, 22], [22, 30]]
    .map(function (p, i) {
      return '<i class="star" style="left:' + p[0] + "%;top:" + p[1] + "%;animation-delay:" + i * 0.6 + 's"></i>';
    })
    .join("");
  // The web app's theme tokens follow <html data-theme>; the phone decides here.
  var dark = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  // Faint doodles behind the page (anchor, compass, shells…), as on the web app.
  if (window.SENTINELA_DOODLES && !document.querySelector(".app-doodles")) {
    document.body.insertAdjacentHTML("afterbegin", window.SENTINELA_DOODLES);
  }
  document.querySelectorAll(".scene").forEach(function (el) {
    // .hero: the full sign-in panorama (art/panorama.js), else the small sea.
    el.innerHTML = el.classList.contains("hero") && window.SENTINELA_PANORAMA ? window.SENTINELA_PANORAMA : stars + gulls + ocean;
  });
})();
