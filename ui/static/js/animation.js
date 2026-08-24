document.addEventListener("DOMContentLoaded", () => {

    /*
     * Generic fade-up reveals.
     */

    const fadeElements =
        document.querySelectorAll(".fade-up");


    if (!("IntersectionObserver" in window)) {

        fadeElements.forEach((element) => {
            element.classList.add("is-visible");
        });

    } else {

        const fadeObserver =
            new IntersectionObserver(
                (entries, observer) => {

                    entries.forEach((entry) => {

                        if (!entry.isIntersecting) {
                            return;
                        }

                        entry.target.classList.add(
                            "is-visible"
                        );

                        observer.unobserve(
                            entry.target
                        );

                    });

                },
                {
                    threshold: 0.12
                }
            );

        fadeElements.forEach((element) => {
            fadeObserver.observe(element);
        });

    }


    const reduceMotion =
        window.matchMedia(
            "(prefers-reduced-motion: reduce)"
        ).matches;


    /*
     * Study match — cards slide in from the
     * sides, the AI Match circle rolls in,
     * then connection + badge complete the
     * reveal (see study-group.css).
     */

    const matchArea =
        document.querySelector(".matching-area");

    if (matchArea) {

        if (
            reduceMotion ||
            !("IntersectionObserver" in window)
        ) {

            matchArea.classList.add("in-view");

        } else {

            const matchObserver =
                new IntersectionObserver(
                    (entries, observer) => {

                        entries.forEach((entry) => {

                            if (!entry.isIntersecting) {
                                return;
                            }

                            entry.target.classList.add(
                                "in-view"
                            );

                            observer.unobserve(
                                entry.target
                            );

                        });

                    },
                    {
                        threshold: 0.35
                    }
                );

            matchObserver.observe(matchArea);

        }

    }


    /*
     * Learning loop — choreographed reveal:
     * when the grid scrolls into view, cards
     * rise and de-blur one after another,
     * then each icon badge pops in.
     */

    const loopGrid =
        document.querySelector(".learning-loop-grid");

    if (!loopGrid) {
        return;
    }


    const cards = Array.from(
        loopGrid.querySelectorAll(".learning-step")
    );


    const revealAll = () => {
        loopGrid.classList.add("in-view");
    };


    if (
        reduceMotion ||
        !("IntersectionObserver" in window)
    ) {
        revealAll();
        return;
    }


    /*
     * Assign stagger order once; CSS uses it
     * for the icon pop delay via --stagger-index.
     */

    cards.forEach((card, index) => {
        card.style.setProperty(
            "--stagger-index",
            String(index)
        );
    });


    let revealTimers = [];


    const gridObserver =
        new IntersectionObserver(
            (entries, observer) => {

                entries.forEach((entry) => {

                    if (!entry.isIntersecting) {
                        return;
                    }

                    loopGrid.classList.add("in-view");

                    /*
                     * Stagger the card transitions,
                     * then clear the delays once each
                     * card has landed so hover stays
                     * instant.
                     */

                    cards.forEach((card, index) => {

                        const delay = index * 90;

                        card.style.transitionDelay =
                            `${delay}ms`;

                        revealTimers.push(
                            setTimeout(() => {
                                card.style.transitionDelay = "";
                            }, delay + 700)
                        );

                    });

                    observer.unobserve(entry.target);

                });

            },
            {
                threshold: 0.18
            }
        );


    gridObserver.observe(loopGrid);


    window.addEventListener("pagehide", () => {
        revealTimers.forEach(clearTimeout);
        revealTimers = [];
    });

});
