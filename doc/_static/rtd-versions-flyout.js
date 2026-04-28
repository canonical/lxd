document.addEventListener("readthedocs-addons-data-ready", function (event) {
    // This script customizes the RTD flyout to show a version label next to "default", based on an RTD environment variable.

    // The RTD flyout addon renders <readthedocs-flyout> before firing this
    // event, so the element is already in the DOM by the time this listener
    // runs. We check immediately, and also watch for any late insertion.

    // Read the version label injected at build time from the FLYOUT_DEFAULT_VERSION_LABEL env var.
    // This var needs to be manually defined in the RTD project dashboard, to whatever should appear in parenthese next to "default" in the flyout, such as "v5.21".
    const versionLabel = window.flyoutDefaultVersionLabel;
    if (!versionLabel) return;

    function patchFlyout(root) {
        let patched = false;
        // Patch the version links in the dropdown list.
        root.querySelectorAll("a").forEach(link => {
            if (link.textContent.trim() === "default") {
                link.textContent = `default (${versionLabel})`;
                patched = true;
            }
        });
        // Patch the "default" label shown on the flyout toggle button.
        const versionSpan = root.querySelector("span.version");
        if (versionSpan) {
            versionSpan.childNodes.forEach(node => {
                if (node.nodeType === Node.TEXT_NODE && node.textContent.trim() === "default") {
                    node.textContent = node.textContent.replace("default", `default (${versionLabel})`);
                    patched = true;
                }
            });
        }
        return patched;
    }

    function watchAndPatch(root) {
        if (patchFlyout(root)) return;
        // Shadow DOM content may not be fully rendered yet; watch for it.
        const observer = new MutationObserver(function () {
            if (patchFlyout(root)) observer.disconnect();
        });
        observer.observe(root, { childList: true, subtree: true });
    }

    // Check immediately — the flyout is likely already in the DOM.
    const flyout = document.querySelector("readthedocs-flyout");
    if (flyout) {
        watchAndPatch(flyout.shadowRoot || flyout);
        return;
    }

    // Fallback: watch for the element to be inserted later.
    const bodyObserver = new MutationObserver(function () {
        const flyout = document.querySelector("readthedocs-flyout");
        if (!flyout) return;
        bodyObserver.disconnect();
        watchAndPatch(flyout.shadowRoot || flyout);
    });
    bodyObserver.observe(document.body, { childList: true, subtree: true });
});