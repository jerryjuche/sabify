document.addEventListener("DOMContentLoaded", () => {

    const menuButton =
        document.querySelector(".mobile-menu-button");

    const mobileNavigation =
        document.querySelector(".mobile-navigation");


    if (!menuButton || !mobileNavigation) {
        return;
    }


    menuButton.addEventListener("click", () => {

        const isOpen =
            menuButton.classList.toggle("is-open");

        mobileNavigation.classList.toggle(
            "is-open",
            isOpen
        );


        menuButton.setAttribute(
            "aria-expanded",
            String(isOpen)
        );

        mobileNavigation.setAttribute(
            "aria-hidden",
            String(!isOpen)
        );
    });


    /*
     * Close the mobile menu after
     * clicking a navigation link.
     */

    const mobileLinks =
        mobileNavigation.querySelectorAll("a");

    mobileLinks.forEach((link) => {

        link.addEventListener("click", () => {

            menuButton.classList.remove("is-open");

            mobileNavigation.classList.remove(
                "is-open"
            );

            menuButton.setAttribute(
                "aria-expanded",
                "false"
            );

            mobileNavigation.setAttribute(
                "aria-hidden",
                "true"
            );
        });

    });


    /*
     * Role links ("For Teachers" / "For Students")
     * scroll to the experiences section and
     * activate the matching tab on arrival.
     */

    const roleLinks =
        document.querySelectorAll("[data-role-link]");

    roleLinks.forEach((link) => {

        link.addEventListener("click", () => {

            const role =
                link.dataset.roleLink;

            const tab = document.querySelector(
                `.intelligence-tab[data-role="${role}"]`
            );

            if (tab && !tab.classList.contains("active")) {
                tab.click();
            }

        });

    });

});