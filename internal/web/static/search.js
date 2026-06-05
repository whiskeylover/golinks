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

  const render = (links) => {
    if (links.length === 0) {
      results.innerHTML = '<p class="empty-links">No matching links.</p>';
      return;
    }

    const items = links.map((link) => {
      const path = shortcutPath(link.shortcut);
      return `<li>
        <span class="shortcut-with-count"><a class="shortcut" href="/${path}">go/${escapeHTML(link.shortcut)}</a><span class="usage-count">${escapeHTML(link.use_count)}</span></span>
        <span class="destination">${escapeHTML(link.destination_url)}</span>
        <a class="edit" href="/edit/${path}">Edit</a>
      </li>`;
    }).join("");
    results.innerHTML = `<ul class="links">${items}</ul>`;
  };

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
