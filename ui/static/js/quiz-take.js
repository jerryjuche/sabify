document.addEventListener("DOMContentLoaded", () => {

    const form = document.getElementById("quiz-form");

    if (!form) {
        return; /* empty-state or no-quiz render */
    }

    const questions = Array.from(
        form.querySelectorAll(".quiz-question")
    );

    if (questions.length === 0) {
        return;
    }


    /*
     * Elements
     */

    const prevBtn = document.getElementById("quiz-prev");
    const nextBtn = document.getElementById("quiz-next");
    const submitBtn = document.getElementById("quiz-submit");
    const palette = document.getElementById("quiz-palette");
    const progressFill = document.getElementById("quiz-progress-fill");
    const currentIndexEl = document.getElementById("quiz-current-index");


    let current = 0;
    let graded = false;


    const reduceMotion =
        window.matchMedia(
            "(prefers-reduced-motion: reduce)"
        ).matches;


    /*
     * Helpers
     */

    const selectedValue = (fieldset) => {

        const checked =
            fieldset.querySelector("input[type=radio]:checked");

        return checked ? checked.value : null;
    };

    const answeredCount = () =>
        questions.filter((q) => selectedValue(q) !== null).length;

    const isLast = (index) => index === questions.length - 1;

    const canGradeInstantly = () =>
        questions.every((q) => q.dataset.correct !== "");


    /*
     * Palette
     */

    const dots = [];

    if (palette) {

        questions.forEach((q, index) => {

            const dot = document.createElement("button");
            dot.type = "button";
            dot.className = "palette-dot";
            dot.textContent = String(index + 1);
            dot.setAttribute(
                "aria-label",
                "Go to question " + (index + 1)
            );

            dot.addEventListener("click", () => {
                goTo(index);
            });

            palette.appendChild(dot);
            dots.push(dot);

        });

    }


    /*
     * Rendering
     */

    const render = (direction) => {

        questions.forEach((q, index) => {

            q.classList.remove("is-active", "is-leaving-back");

            if (index === current) {

                q.classList.add("is-active");

                if (
                    direction === "back" &&
                    !reduceMotion
                ) {
                    q.classList.add("is-leaving-back");
                }

            }

        });

        currentIndexEl.textContent = String(current + 1);

        progressFill.style.width =
            Math.round(((current + 1) / questions.length) * 100) + "%";

        prevBtn.disabled = current === 0;
        nextBtn.style.display = isLast(current) ? "none" : "";
        submitBtn.classList.toggle(
            "is-visible",
            isLast(current)
        );

        dots.forEach((dot, index) => {
            dot.classList.toggle("is-current", index === current);
            dot.classList.toggle(
                "is-answered",
                selectedValue(questions[index]) !== null
            );
        });

    };

    const goTo = (index, direction) => {

        if (index < 0 || index >= questions.length) {
            return;
        }

        current = index;

        render(direction);

    };


    prevBtn.addEventListener("click", () => {
        goTo(current - 1, "back");
    });

    nextBtn.addEventListener("click", () => {
        goTo(current + 1, "forward");
    });


    form.addEventListener("change", () => {
        dots.forEach((dot, index) => {
            dot.classList.toggle(
                "is-answered",
                selectedValue(questions[index]) !== null
            );
        });
    });


    /*
     * While JavaScript is running, every native submission
     * is intercepted — including Enter-key submits. Grading
     * happens client side and the attempt is recorded via
     * fetch() instead.
     */

    form.addEventListener("submit", (event) => {
        event.preventDefault();
    });


    /*
     * Keyboard shortcuts:
     * 1–4 select an option, arrows navigate.
     */

    document.addEventListener("keydown", (event) => {

        if (!modal.hidden) {
            return;
        }

        if (graded) {
            return;
        }

        const activeFieldset =
            questions[current];

        const tag =
            event.target.tagName;

        if (
            tag === "INPUT" ||
            tag === "TEXTAREA"
        ) {
            return; /* radios keep native behaviour */
        }

        if (
            event.key === "ArrowRight"
        ) {
            goTo(current + 1, "forward");
            return;
        }

        if (
            event.key === "ArrowLeft"
        ) {
            goTo(current - 1, "back");
            return;
        }

        const keyIndex =
            ["1", "2", "3", "4"].indexOf(event.key);

        if (
            keyIndex >= 0 &&
            activeFieldset
        ) {

            const option = activeFieldset.querySelectorAll(
                "input[type=radio]"
            )[keyIndex];

            if (option) {
                option.checked = true;
                option.dispatchEvent(
                    new Event("change", { bubbles: true })
                );
            }

        }

    });


    /*
     * Submit modal
     */

    const modal = document.getElementById("submit-modal");
    const modalText = document.getElementById("modal-text");
    const cancelBtn = document.getElementById("modal-cancel");
    const confirmBtn = document.getElementById("modal-confirm");


    const openModal = () => {

        const unanswered =
            questions.length - answeredCount();

        modalText.innerHTML =
            unanswered > 0

                ? "You have <strong>" + unanswered +
                  "</strong> unanswered question" +
                  (unanswered === 1 ? "" : "s") +
                  ". They will be marked as skipped."

                : "All questions answered. Ready to hand it in?";

        modal.hidden = false;
        confirmBtn.focus();

    };

    const closeModal = () => {
        modal.hidden = true;
        submitBtn.focus();
    };

    /*
     * preventDefault is critical: this is a type=submit
     * button, so without it the browser performs a native
     * form navigation instead of showing the modal.
     */

    submitBtn.addEventListener("click", (event) => {
        event.preventDefault();
        openModal();
    });
    cancelBtn.addEventListener("click", closeModal);

    modal.addEventListener("click", (event) => {
        if (event.target === modal) {
            closeModal();
        }
    });

    document.addEventListener("keydown", (event) => {
        if (event.key === "Escape" && !modal.hidden) {
            closeModal();
        }
    });


    /*
     * Grading + result rendering
     */

    const ringFill =
        document.getElementById("score-ring-fill");

    const scoreValue =
        document.getElementById("score-value");

    const RING_LENGTH = 339.29;

    const animateScore = (pct, counts) => {

        const heading =
            document.getElementById("result-heading");

        document.getElementById("chip-correct").textContent =
            String(counts.correct);

        document.getElementById("chip-wrong").textContent =
            String(counts.wrong);

        document.getElementById("chip-skipped").textContent =
            String(counts.skipped);

        if (
            pct >= 80
        ) {
            heading.textContent = "Outstanding work!";
        } else if (
            pct >= 50
        ) {
            heading.textContent = "Nice effort!";
        } else {
            heading.textContent = "Keep practising!";
        }

        if (
            reduceMotion ||
            !ringFill ||
            !scoreValue
        ) {

            if (ringFill && scoreValue) {
                ringFill.style.strokeDashoffset =
                    String(RING_LENGTH * (1 - pct / 100));
                scoreValue.textContent = pct + "%";
            }

            return;
        }

        requestAnimationFrame(() => {
            ringFill.style.strokeDashoffset =
                String(RING_LENGTH * (1 - pct / 100));
        });

        const duration = 900;
        const start = performance.now();

        const tick = (now) => {

            const progress =
                Math.min((now - start) / duration, 1);

            const eased =
                1 - Math.pow(1 - progress, 3);

            scoreValue.textContent =
                Math.round(pct * eased) + "%";

            if (progress < 1) {
                requestAnimationFrame(tick);
            }
        };

        requestAnimationFrame(tick);

    };

    const buildReview = () => {

        const list =
            document.getElementById("review-list");

        list.innerHTML = "";

        questions.forEach((q, index) => {

            const picked = selectedValue(q);
            const correct = q.dataset.correct;

            const li = document.createElement("li");

            let stateClass = "is-skipped";
            let pickedHtml =
                '<span class="review-answer is-skipped">' +
                "<strong>Skipped</strong> — no answer given</span>";

            if (picked !== null) {

                if (picked === correct) {
                    stateClass = "is-correct";
                    pickedHtml =
                        '<span class="review-answer is-correct">' +
                        "<strong>Your answer:</strong> " +
                        picked + "</span>";
                } else {
                    stateClass = "is-wrong";
                    pickedHtml =
                        '<span class="review-answer is-wrong">' +
                        "<strong>Your answer:</strong> " +
                        picked + "</span>";
                }

            }

            li.className = "review-item " + stateClass;

            li.innerHTML =
                '<p class="review-q">' +
                (index + 1) + ". " +
                q.querySelector(".quiz-question-text").textContent.replace(/^\d+\.\s*/, "") +
                "</p>" +
                '<div class="review-answer-row">' +
                pickedHtml +
                '<span class="review-answer"><strong>Correct:</strong> ' +
                correct + "</span>" +
                "</div>";

            list.appendChild(li);

        });

    };

    const recordAttempt = () => {

        const status =
            document.getElementById("save-status");

        fetch(form.action, {
            method: "POST",
            body: new FormData(form)
        })

            .then((response) => {

                if (!response.ok) {
                    throw new Error("HTTP " + response.status);
                }

                status.textContent =
                    "Your attempt has been recorded.";

            })

            .catch(() => {

                status.textContent =
                    "Offline demo — attempt not saved to the server yet.";

            });

    };

    const gradeNow = () => {

        graded = true;

        const counts = { correct: 0, wrong: 0, skipped: 0 };

        questions.forEach((q) => {

            const picked = selectedValue(q);

            if (picked === null) {
                counts.skipped++;
            } else if (
                picked === q.dataset.correct
            ) {
                counts.correct++;
            } else {
                counts.wrong++;
            }

        });

        const total = questions.length;

        const pct = total > 0
            ? Math.round(
                (counts.correct / total) * 100
            )
            : 0;

        animateScore(pct, counts);

        buildReview();

        document.getElementById("result-view").hidden = false;

        document.getElementById("quiz-view").hidden = true;

        window.scrollTo({
            top: 0,
            behavior: reduceMotion ? "auto" : "smooth"
        });

        recordAttempt();

    };

    confirmBtn.addEventListener("click", () => {

        closeModal();

        if (
            canGradeInstantly()
        ) {
            gradeNow();
            return;
        }

        /* No correct answers provided: fall back to the server. */
        form.submit();

    });

    render();

});
