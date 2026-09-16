// Progressive scroll-reveal. <html> is marked .js by an inline head script so
// CSS can pre-hide .reveal elements; this module then reveals them on scroll.
// Fails open: nothing can stay hidden.

function start() {
  const els = Array.from(document.querySelectorAll<HTMLElement>(".reveal"));
  if (!els.length) return;

  const show = (el: Element) => el.classList.add("in");

  // Anything at or above the current viewport (plus a generous buffer) is shown
  // immediately on the next frame so it still transitions in.
  const eager = window.innerHeight * 1.15;
  const pending: HTMLElement[] = [];
  for (const el of els) {
    if (el.getBoundingClientRect().top < eager) {
      requestAnimationFrame(() => show(el));
    } else {
      pending.push(el);
    }
  }

  if (!("IntersectionObserver" in window)) {
    pending.forEach(show);
    return;
  }

  const io = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting) {
          show(entry.target);
          io.unobserve(entry.target);
        }
      }
    },
    { rootMargin: "0px 0px -6% 0px", threshold: 0.04 },
  );
  pending.forEach((el) => io.observe(el));

  // Failsafe: never leave content hidden.
  window.setTimeout(() => els.forEach(show), 1200);
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", start);
} else {
  start();
}
