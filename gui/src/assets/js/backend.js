// Axios joins relative URLs to baseURL (including URLs starting with a slash).
export function isBackendRequest(config) {
  if (!config || typeof config.url !== "string") return false;
  let url = config.url;
  if (config.baseURL && !/^([a-z][a-z\d+.-]*:)?\/\//i.test(url)) {
    url = config.baseURL.replace(/\/+$/, "") + "/" + url.replace(/^\/+/, "");
  }
  try {
    const root = new URL(apiRoot, document.baseURI);
    const target = new URL(url, document.baseURI);
    return (
      (root.protocol === "http:" || root.protocol === "https:") &&
      target.origin === root.origin &&
      (target.pathname === root.pathname || target.pathname.startsWith(root.pathname + "/"))
    );
  } catch {
    return false;
  }
}

export function setBackendAddress(address) {
  const current = localStorage.getItem("backendAddress") || "";
  if (address !== current) {
    const next = new URL(address + "/api", document.baseURI);
    let previous;
    try {
      previous = new URL(current + "/api", document.baseURI);
    } catch {
      // Allow a valid address to replace a malformed saved setting.
    }
    if (previous?.href !== next.href) localStorage.removeItem("token");
  }
  localStorage.setItem("backendAddress", address);
}
