// Reveal-on-scroll: one observer, fires once per element. Elements are visible
// by default; only `html.js [data-reveal]` hides them (see global.css), so a
// no-JS or reduced-motion visitor never sees blank sections.
document.documentElement.classList.add('reveal-ready');
const els = document.querySelectorAll<HTMLElement>('[data-reveal]');
const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
if (!('IntersectionObserver' in window) || reduce) {
  els.forEach((el) => el.classList.add('is-visible'));
} else {
  const io = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (e.isIntersecting) {
          e.target.classList.add('is-visible');
          io.unobserve(e.target);
        }
      }
    },
    { rootMargin: '0px 0px -8% 0px', threshold: 0.08 },
  );
  els.forEach((el) => io.observe(el));
}
