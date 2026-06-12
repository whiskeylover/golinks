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
      const favoriteLabel = link.is_favorite ? "Unfavorite" : "Favorite";
      const favoriteClass = link.is_favorite ? " is-pinned" : "";
      const temporaryClass = link.expires_date ? " is-temporary" : "";
      const temporaryIcon = link.expires_date ? `<span aria-label="Temporary link, expires on ${escapeHTML(link.expires_date)}" class="temp-indicator icon-button" role="img" title="Expires on ${escapeHTML(link.expires_date)}"><svg aria-hidden="true" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6"></circle><path d="M8 4v4l2.5 1.5"></path></svg></span>` : "";
      return `<li>
        <span class="shortcut-with-count${temporaryClass}"><a class="shortcut" href="/${path}">go/${escapeHTML(link.shortcut)}</a><span class="link-badges"><span class="usage-count">${escapeHTML(link.use_count)}</span></span></span>
        <span class="destination">${escapeHTML(link.destination_url)}</span>
        <span class="row-actions">
          ${temporaryIcon}
          <form class="pin-form" action="/favorite/${path}" method="post">
            <input name="favorite" type="hidden" value="${favoriteValue}">
            <button aria-label="${favoriteLabel} go/${escapeHTML(link.shortcut)}" class="pin-button${favoriteClass}" title="${favoriteLabel}" type="submit"><svg aria-hidden="true" viewBox="0 0 16 16"><path d="M8 1.4 10 5.6l4.6.7-3.3 3.2.8 4.6L8 11.9l-4.1 2.2.8-4.6-3.3-3.2 4.6-.7L8 1.4Z"></path></svg></button>
          </form>
          <button aria-label="Copy go/${escapeHTML(link.shortcut)}" class="copy-button icon-button" data-copy-path="/${path}" title="Copy" type="button"><svg aria-hidden="true" viewBox="0 0 16 16"><path d="M6 1h7v9H6z"></path><path d="M3 5h7v10H3z"></path></svg></button>
          <button aria-label="QR code for go/${escapeHTML(link.shortcut)}" class="qr-button icon-button" data-qr-path="/${path}" title="QR" type="button"><svg aria-hidden="true" viewBox="0 0 16 16"><path d="M2 2h4v4H2zM10 2h4v4h-4zM2 10h4v4H2zM10 10h1v1h-1zM13 10h1v1h-1zM10 13h4v1h-4zM13 11h1v2h-1z"></path></svg></button>
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

  let qrDialog;
  const ensureQRDialog = () => {
    if (qrDialog) {
      return qrDialog;
    }
    qrDialog = document.createElement("div");
    qrDialog.className = "qr-modal";
    qrDialog.hidden = true;
    qrDialog.innerHTML = `<div class="qr-card" role="dialog" aria-modal="true" aria-labelledby="qr-title">
      <button aria-label="Close QR code" class="qr-close" type="button">&times;</button>
      <h2 id="qr-title" class="qr-title"></h2>
      <div class="qr-code" data-qr-code></div>
    </div>`;
    document.body.append(qrDialog);
    qrDialog.addEventListener("click", (event) => {
      if (event.target === qrDialog || event.target.closest(".qr-close")) {
        qrDialog.hidden = true;
      }
    });
    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape") {
        qrDialog.hidden = true;
      }
    });
    return qrDialog;
  };

  const showQRCode = (url, label) => {
    const dialog = ensureQRDialog();
    const code = dialog.querySelector("[data-qr-code]");
    const title = dialog.querySelector("#qr-title");
    if (typeof qrcode !== "function") {
      code.textContent = "QR unavailable.";
    } else {
      const qr = qrcode(0, "M");
      qr.addData(url);
      qr.make();
      code.innerHTML = qr.createSvgTag(5, 3, url, url);
    }
    title.textContent = label;
    dialog.hidden = false;
    dialog.querySelector(".qr-close").focus();
  };

  results.addEventListener("click", (event) => {
    const button = event.target.closest("[data-qr-path]");
    if (!button) {
      return;
    }
    showQRCode(
      new URL(button.dataset.qrPath, window.location.origin).href,
      `go${button.dataset.qrPath}`,
    );
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
