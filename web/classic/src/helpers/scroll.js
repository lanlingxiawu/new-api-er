export function scrollDocumentToTop(behavior = 'auto') {
  const scrollOptions = { top: 0, left: 0, behavior };
  const targets = [
    document.querySelector('.app-layout-scroll'),
    document.scrollingElement,
    window,
  ];

  targets.forEach((target) => {
    if (!target) return;
    if (typeof target.scrollTo === 'function') {
      target.scrollTo(scrollOptions);
      return;
    }
    target.scrollTop = 0;
    target.scrollLeft = 0;
  });
}
