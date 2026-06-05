(() => {
  const input = document.querySelector("[data-link-search]");
  const results = document.querySelector("[data-link-results]");
  if (!input || !results) {
    return;
  }

  const topLinksHTML = results.innerHTML;
  let controller;
  let timeout;

  const escapeHTML = (value) => {
    const element = document.createElement("div");
    element.textContent = String(value);
    return element.innerHTML;
  };

  const shortcutPath = (shortcut) => shortcut
    .split("/")
    .map(encodeURIComponent)
    .join("/");

  const fallbackCopy = (text) => {
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.setAttribute("readonly", "");
    textarea.style.position = "fixed";
    textarea.style.opacity = "0";
    document.body.append(textarea);
    textarea.select();
    try {
      document.execCommand("copy");
    } finally {
      textarea.remove();
    }
  };

  const copyText = async (text) => {
    if (navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(text);
        return;
      } catch {
        // Fall back for plain-http hosts where Clipboard API can be unavailable.
      }
    }
    fallbackCopy(text);
  };

  const render = (links) => {
    if (links.length === 0) {
      results.innerHTML = '<p class="empty-links">No matching links.</p>';
      return;
    }

    const items = links.map((link) => {
      const path = shortcutPath(link.shortcut);
      const favoriteValue = link.is_favorite ? "0" : "1";
      const favoriteLabel = link.is_favorite ? "Unpin" : "Pin";
      const favoriteClass = link.is_favorite ? " is-pinned" : "";
      return `<li>
        <span class="shortcut-with-count"><a class="shortcut" href="/${path}">go/${escapeHTML(link.shortcut)}</a><span class="usage-count">${escapeHTML(link.use_count)}</span></span>
        <span class="destination">${escapeHTML(link.destination_url)}</span>
        <span class="row-actions">
          <form class="pin-form" action="/favorite/${path}" method="post">
            <input name="favorite" type="hidden" value="${favoriteValue}">
            <button aria-label="${favoriteLabel} go/${escapeHTML(link.shortcut)}" class="pin-button${favoriteClass}" title="${favoriteLabel}" type="submit"><svg aria-hidden="true" viewBox="0 0 16 16"><path d="M5 1h6l-1 5 3 3v1H9l-1 5H7l-1-5H2V9l3-3-1-5Z"></path></svg></button>
          </form>
          <button aria-label="Copy go/${escapeHTML(link.shortcut)}" class="copy-button icon-button" data-copy-path="/${path}" title="Copy" type="button"><svg aria-hidden="true" viewBox="0 0 16 16"><path d="M6 1h7v9H6z"></path><path d="M3 5h7v10H3z"></path></svg></button>
          <a aria-label="Edit go/${escapeHTML(link.shortcut)}" class="edit icon-link" href="/edit/${path}" title="Edit"><svg aria-hidden="true" viewBox="0 0 16 16"><path d="M11.8 1.7a1.6 1.6 0 0 1 2.3 2.3l-8.5 8.5-3.1.8.8-3.1 8.5-8.5Zm-1.1 2.8.8.8"></path></svg></a>
        </span>
      </li>`;
    }).join("");
    results.innerHTML = `<ul class="links">${items}</ul>`;
  };

  results.addEventListener("click", async (event) => {
    const button = event.target.closest("[data-copy-path]");
    if (!button) {
      return;
    }
    const copyURL = new URL(button.dataset.copyPath, window.location.origin).href;
    await copyText(copyURL);
    button.classList.add("is-copied");
    button.title = "Copied";
    clearTimeout(button.copyTimeout);
    button.copyTimeout = setTimeout(() => {
      button.classList.remove("is-copied");
      button.title = "Copy";
    }, 1200);
  });

  const search = async (query) => {
    controller?.abort();
    controller = new AbortController();
    try {
      const response = await fetch(`/api/links?q=${encodeURIComponent(query)}`, {
        headers: { Accept: "application/json" },
        signal: controller.signal,
      });
      if (!response.ok) {
        throw new Error(`search failed: ${response.status}`);
      }
      render(await response.json());
    } catch (error) {
      if (error.name !== "AbortError") {
        results.innerHTML = '<p class="empty-links">Search unavailable.</p>';
      }
    }
  };

  input.addEventListener("input", () => {
    clearTimeout(timeout);
    const query = input.value.trim();
    if (query === "") {
      controller?.abort();
      results.innerHTML = topLinksHTML;
      return;
    }
    timeout = setTimeout(() => search(query), 150);
  });
})();
