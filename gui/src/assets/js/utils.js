import CONST from "./const.js";
import URI from "urijs";

function _locateServer(touch, whichServer) {
  if (!touch || !whichServer) {
    return null;
  }
  let ind = whichServer.id - 1;
  let sub = whichServer.sub;
  if (whichServer._type === CONST.ServerType) {
    return touch.servers?.[ind] || null;
  } else if (whichServer._type === CONST.SubscriptionServerType) {
    return touch.subscriptions?.[sub]?.servers?.[ind] || null;
  }
  return null;
}

function locateServer(touch, whichServer) {
  if (whichServer instanceof Array) {
    const servers = [];
    for (const w of whichServer) {
      const server = _locateServer(touch, w);
      if (server) {
        servers.push(server);
      }
    }
    return servers;
  }
  return _locateServer(touch, whichServer);
}

function handleResponse(res, that, suc, err, fail) {
  if (!res.data) {
    if (err && err instanceof Function) {
      err.apply(that);
    }
    return;
  }
  if (res.data.code === "SUCCESS") {
    suc.apply(that);
  } else {
    if (err && err instanceof Function) {
      err.apply(that);
    } else {
      if (fail && fail instanceof Function) {
        fail.apply(that);
      } else {
        that.$buefy.toast.open({
          message: res.data.message,
          type: "is-warning",
          position: "is-top",
          queue: false,
          duration: 5000,
        });
      }
    }
  }
}

/*
var myURL = parseURL('http://user:pass@abc.com:8080/dir/index.html?id=255&m=hello#top');
myURL.username;     // = 'user'
myURL.password;     // = 'pass'
myURL.file;     // = 'index.html'
myURL.hash;     // = 'top'
myURL.host;     // = 'abc.com'
myURL.query;    // = '?id=255&m=hello'
myURL.params;   // = Object = { id: 255, m: hello }
myURL.path;     // = '/dir/index.html'
myURL.segments; // = Array = ['dir', 'index.html']
myURL.port;     // = '8080'
myURL.protocol; // = 'http'
myURL.source;   // = 'http://user:pass@abc.com:8080/dir/index.html?id=255&m=hello#top'
*/
function parseURL(u) {
  const source = u.includes("://") || u.startsWith("/") ? u : "http://" + u;
  const a = new URL(source, window.location.origin);
  const protocol = a.protocol.slice(0, -1);
  const defaultPort = ["https", "https-proxy", "wss", "hy2", "hysteria2"].includes(protocol) ? 443 : 80;
  const params = Object.create(null);
  for (const [key, value] of a.searchParams) {
    if (params[key] === undefined) params[key] = value;
    else if (Array.isArray(params[key])) params[key].push(value);
    else params[key] = [params[key], value];
  }
  const r = {
    source: u,
    username: a.username,
    password: a.password,
    protocol,
    host: a.hostname,
    port: a.port ? Number(a.port) : defaultPort,
    query: a.search,
    params,
    file: (a.pathname.match(/\/([^/?#]+)$/i) || [null, ""])[1],
    hash: a.hash.replace("#", ""),
    path: a.pathname.replace(/^([^/])/, "/$1"),
    relative: a.pathname + a.search + a.hash,
    segments: a.pathname.replace(/^\//, "").split("/"),
  };
  return r;
}

function generateURL({
  username,
  password,
  protocol,
  host,
  port,
  params,
  hash,
  path,
}) {
  /**
   * 所有参数设置默认值
   * 避免方法检测到参数为null/undefine返回该值查询结果
   * 查询结果当然不是URI类型，导致链式调用失败
   */
  const uri = URI()
    .protocol(protocol || "http")
    .username(username || "")
    .password(password || "")
    .host(host || "")
    .port(port || 80)
    .path(path || "")
    .query(params || {})
    .hash(hash ? encodeURIComponent(hash) : "");
  const res = uri.toString();
  return res;
}

function toInt(s) {
  if (typeof s === "string") {
    return parseInt(s);
  } else if (typeof s === "number") {
    return parseInt(s);
  } else if (typeof s === "boolean") {
    return s ? 1 : 0;
  }
  return s;
}

function sanitizeALPN(alpn) {
  if (!alpn) return "h3"; // Default to "h3" if no ALPN is provided
  return alpn; // Retain the original input
}

export { locateServer, handleResponse, parseURL, generateURL, toInt, sanitizeALPN };
