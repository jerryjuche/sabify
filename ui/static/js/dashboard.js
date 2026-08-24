document.addEventListener("DOMContentLoaded", () => {

    /*
     * Mobile drawer.
     */

    const sidebar = document.getElementById("dash-sidebar");
    const menuButton = document.querySelector(".dash-menu-button");
    const backdrop = document.querySelector("[data-drawer-close]");

    if (sidebar && menuButton && backdrop) {

        const openDrawer = () => {
            sidebar.classList.add("is-open");
            backdrop.classList.add("is-visible");
            menuButton.setAttribute("aria-expanded", "true");
            document.body.classList.add("drawer-open");
        };

        const closeDrawer = () => {
            sidebar.classList.remove("is-open");
            backdrop.classList.remove("is-visible");
            menuButton.setAttribute("aria-expanded", "false");
            document.body.classList.remove("drawer-open");
        };

        menuButton.addEventListener("click", () => {
            if (sidebar.classList.contains("is-open")) {
                closeDrawer();
            } else {
                openDrawer();
            }
        });

        backdrop.addEventListener("click", closeDrawer);

        document.addEventListener("keydown", (event) => {
            if (event.key === "Escape") {
                closeDrawer();
            }
        });

    }


    /*
     * Stat count-up. Runs once, when the stats
     * grid first scrolls into view. Respects
     * prefers-reduced-motion.
     */

    const reduceMotion =
        window.matchMedia(
            "(prefers-reduced-motion: reduce)"
        ).matches;

    const counters =
        document.querySelectorAll("[data-countup]");

    if (counters.length > 0) {

        const animateCounter = (element) => {

            const target =
                parseInt(element.dataset.countup, 10) || 0;

            const suffix =
                element.dataset.suffix || "";

            if (
                reduceMotion ||
                !("requestAnimationFrame" in window) ||
                target === 0
            ) {
                element.textContent = target + suffix;
                return;
            }

            const duration = 900;
            const start = performance.now();

            const tick = (now) => {

                const progress =
                    Math.min((now - start) / duration, 1);

                /* easeOutCubic */
                const eased =
                    1 - Math.pow(1 - progress, 3);

                element.textContent =
                    Math.round(target * eased) + suffix;

                if (progress < 1) {
                    requestAnimationFrame(tick);
                }
            };

            requestAnimationFrame(tick);
        };


        if (!("IntersectionObserver" in window)) {

            counters.forEach(animateCounter);

        } else {

            const counterObserver =
                new IntersectionObserver(
                    (entries, observer) => {

                        entries.forEach((entry) => {

                            if (!entry.isIntersecting) {
                                return;
                            }

                            animateCounter(entry.target);
                            observer.unobserve(entry.target);

                        });

                    },
                    {
                        threshold: 0.4
                    }
                );

            counters.forEach((counter) => {
                counterObserver.observe(counter);
            });

        }

    }

});
