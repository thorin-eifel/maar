// Theme vor dem ersten Zeichnen setzen (kein Aufblitzen).
(function () {
  var root = document.documentElement;
  try {
    var t = localStorage.getItem('maar.theme');
    if (t === 'dark' || t === 'light') root.dataset.theme = t;
  } catch (e) {}
})();
