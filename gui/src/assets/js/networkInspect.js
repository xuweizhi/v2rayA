import axios from "../../plugins/axios";
import { handleResponse } from "./utils";

// Poll /touch until backend reports running state or the main request settles
function waitingConnected(promise, interval, cancel, timeout) {
  let stopped = false;
  let timer = null;
  let timeoutTimer = null;

  const cleanup = () => {
    stopped = true;
    if (timer !== null) {
      clearInterval(timer);
      timer = null;
    }
    if (timeoutTimer !== null) {
      clearTimeout(timeoutTimer);
      timeoutTimer = null;
    }
  };

  timer = setInterval(() => {
    if (stopped) {
      return;
    }
    axios({
      url: apiRoot + "/touch",
      timeout: interval,
    })
      .then((res) => {
        if (stopped) {
          return;
        }
        handleResponse(
          res,
          null,
          () => {
            if (res.data.data?.running && res.data.data?.touch?.connectedServer) {
              cleanup();
              // Do NOT call cancel() here — cancelling rejects the caller's promise
              // and skips its .then() handler. Let the main request complete normally.
            }
          },
          () => {
            // Skip toast in standalone poller; the main request handles error reporting.
          }
        );
      })
      .catch((err) => {
        if (err.response?.status === 401) {
          cleanup();
          cancel && cancel();
        }
      });
  }, interval);

  timeoutTimer = setTimeout(() => {
    cleanup();
  }, timeout || 30 * 1000);

  if (promise && typeof promise.finally === "function") {
    promise.finally(cleanup).catch(() => {});
  }
}

export { waitingConnected };
