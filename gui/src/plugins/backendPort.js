import { setBackendAddress } from "@/assets/js/backend";
console.log("backendPort");
let ba = localStorage.getItem("backendAddress");
let currentPrefix = "";
if (typeof window !== "undefined") {
  let path = window.location.pathname;
  const match = path.match(/^(.*)\/(?:login|setting|log|server|rule|running)?\/?$/);
  if (match) {
    currentPrefix = match[1];
  }
}
if (currentPrefix && currentPrefix !== "/") {
  setBackendAddress(currentPrefix);
} else {
  if (ba == null) {
    setBackendAddress("");
  }
}
