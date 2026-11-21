const STORAGE_FONT_PRESET = "va_font_preset";
const STORAGE_FONT_CUSTOM = "va_font_custom";
const STORAGE_CUSTOM_FONT_PRESETS = "va_font_custom_presets";
const STORAGE_FONT_SIZE = "va_font_size";
const STORAGE_LAYOUT_WIDTH = "va_layout_width";
const STORAGE_UI_STYLE = "va_ui_style";

export const MAX_CUSTOM_FONT_PRESETS = 20;
export const CUSTOM_FONT_PRESET_PREFIX = "preset:";

export const UI_STYLE_PRESETS = [
  {
    id: "precision",
    badge: "A",
    labelKey: "appearance.styles.precision.name",
    descKey: "appearance.styles.precision.desc",
  },
  {
    id: "soft",
    badge: "B",
    labelKey: "appearance.styles.soft.name",
    descKey: "appearance.styles.soft.desc",
  },
  {
    id: "cyber",
    badge: "C",
    labelKey: "appearance.styles.cyber.name",
    descKey: "appearance.styles.cyber.desc",
  },
];

export const LAYOUT_WIDTH_PRESETS = [
  { id: "compact", labelKey: "appearance.widths.compact", value: "1280px" },
  { id: "wide", labelKey: "appearance.widths.wide", value: "1680px" },
  { id: "full", labelKey: "appearance.widths.full", value: "100%" },
];

export const DEFAULT_FONT_FAMILY =
  'system-ui, "Microsoft YaHei UI", "Source Han Sans CN", "Twemoji Mozilla", sans-serif';

export const FONT_PRESETS = [
  {
    id: "system",
    labelKey: "appearance.fonts.system",
    stack: DEFAULT_FONT_FAMILY,
  },
  {
    id: "modern",
    labelKey: "appearance.fonts.modern",
    stack:
      '"Inter", "SF Pro Text", "PingFang SC", "HarmonyOS Sans SC", "MiSans", "Noto Sans SC", "Segoe UI", sans-serif',
  },
  {
    id: "rounded",
    labelKey: "appearance.fonts.rounded",
    stack:
      '"SF Pro Rounded", "Quicksand", "Hiragino Maru Gothic ProN", "YouYuan", "PingFang SC", "Noto Sans SC", ui-rounded, sans-serif',
  },
  {
    id: "serif",
    labelKey: "appearance.fonts.serif",
    stack:
      '"Noto Serif SC", "Source Han Serif SC", "Songti SC", "STSong", "SimSun", Georgia, "Times New Roman", serif',
  },
  {
    id: "mono",
    labelKey: "appearance.fonts.mono",
    stack:
      '"JetBrains Mono", "Fira Code", "Cascadia Code", "Sarasa Mono SC", "SF Mono", Consolas, ui-monospace, monospace',
  },
  {
    id: "custom",
    labelKey: "appearance.fonts.custom",
    stack: DEFAULT_FONT_FAMILY,
  },
];

const LEGACY_FONT_PRESET_MAP = {
  inter: "modern",
  "sans-cn": "modern",
};

export function customFontPresetKey(id) {
  return `${CUSTOM_FONT_PRESET_PREFIX}${id}`;
}

export function suggestFontPresetLabel(fontFamily) {
  if (typeof fontFamily !== "string") return "";
  const first = (fontFamily.split(",")[0] || "").trim().replace(/^['"]|['"]$/g, "");
  return first.slice(0, 32);
}

export function normalizeFontStylesheetUrl(raw) {
  if (typeof raw !== "string") return undefined;
  const trimmed = raw.trim();
  if (!trimmed) return undefined;
  if (trimmed.startsWith("/") && !trimmed.startsWith("//")) return trimmed;
  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol === "https:" || parsed.protocol === "http:") {
      return parsed.toString();
    }
  } catch (e) {
    return undefined;
  }
  return undefined;
}

export function normalizeCustomFontPresets(raw) {
  if (!Array.isArray(raw)) return [];
  const seenIds = new Set();
  const seenFamilies = new Set();
  const result = [];
  for (const entry of raw) {
    if (!entry || typeof entry !== "object" || Array.isArray(entry)) continue;
    const label = typeof entry.label === "string" ? entry.label.trim().slice(0, 32) : "";
    const fontFamily =
      typeof entry.fontFamily === "string" ? entry.fontFamily.trim().slice(0, 240) : "";
    if (!label || !fontFamily || seenFamilies.has(fontFamily)) continue;
    let id =
      typeof entry.id === "string" && entry.id.trim()
        ? entry.id.trim().slice(0, 48)
        : `custom-font-${result.length + 1}`;
    while (seenIds.has(id)) {
      id = `${id}-${result.length + 1}`;
    }
    seenIds.add(id);
    seenFamilies.add(fontFamily);
    const stylesheetUrl = normalizeFontStylesheetUrl(entry.stylesheetUrl);
    result.push(
      stylesheetUrl ? { id, label, fontFamily, stylesheetUrl } : { id, label, fontFamily }
    );
    if (result.length >= MAX_CUSTOM_FONT_PRESETS) break;
  }
  return result;
}

export function createCustomFontPresetId(existing = []) {
  const taken = new Set(existing.map((item) => item.id));
  let index = existing.length + 1;
  while (taken.has(`custom-font-${index}`)) {
    index += 1;
  }
  return `custom-font-${index}`;
}

export function formatFontFamilyWithFallback(fontFamily) {
  const cleaned = (fontFamily || "").trim().replace(/,\s*$/, "").trim();
  if (!cleaned) return DEFAULT_FONT_FAMILY;
  return `${cleaned}, ${DEFAULT_FONT_FAMILY}`;
}

export function loadCustomFontPresets() {
  if (typeof localStorage === "undefined") return [];
  try {
    const raw = localStorage.getItem(STORAGE_CUSTOM_FONT_PRESETS);
    if (!raw) return [];
    return normalizeCustomFontPresets(JSON.parse(raw));
  } catch (e) {
    return [];
  }
}

function normalizeFontPreset(preset, customFontPresets = []) {
  const mapped = LEGACY_FONT_PRESET_MAP[preset] || preset;
  if (FONT_PRESETS.some((p) => p.id === mapped)) {
    return mapped;
  }
  if (typeof mapped === "string" && mapped.startsWith(CUSTOM_FONT_PRESET_PREFIX)) {
    const exists = customFontPresets.some((item) => customFontPresetKey(item.id) === mapped);
    if (exists) return mapped;
  }
  return "system";
}

export const FONT_SIZE_MIN = 12;
export const FONT_SIZE_MAX = 22;

export function getAppearanceSettings() {
  const customFontPresets = loadCustomFontPresets();
  const preset = localStorage.getItem(STORAGE_FONT_PRESET) || "system";
  const customFont = localStorage.getItem(STORAGE_FONT_CUSTOM) || "";
  const rawSize = localStorage.getItem(STORAGE_FONT_SIZE);
  let fontSize = null;
  if (rawSize !== null && rawSize !== "" && rawSize !== "auto") {
    const parsed = parseInt(rawSize, 10);
    if (Number.isFinite(parsed)) {
      fontSize = Math.min(FONT_SIZE_MAX, Math.max(FONT_SIZE_MIN, parsed));
    }
  }
  const validPreset = normalizeFontPreset(preset, customFontPresets);
  const rawWidth = localStorage.getItem(STORAGE_LAYOUT_WIDTH) || "wide";
  const layoutWidth = LAYOUT_WIDTH_PRESETS.some((w) => w.id === rawWidth) ? rawWidth : "wide";
  const rawStyle = localStorage.getItem(STORAGE_UI_STYLE) || "precision";
  const uiStyle = UI_STYLE_PRESETS.some((s) => s.id === rawStyle) ? rawStyle : "precision";
  return {
    uiStyle,
    fontPreset: validPreset,
    customFont,
    customFontPresets,
    fontSize,
    layoutWidth,
  };
}

export function resolveFontFamily(fontPreset, customFont, customFontPresets) {
  const presets = Array.isArray(customFontPresets)
    ? customFontPresets
    : loadCustomFontPresets();
  const normalized = normalizeFontPreset(fontPreset, presets);
  if (normalized === "custom") {
    return formatFontFamilyWithFallback(customFont);
  }
  if (normalized.startsWith(CUSTOM_FONT_PRESET_PREFIX)) {
    const customPreset = presets.find((p) => customFontPresetKey(p.id) === normalized);
    return customPreset
      ? formatFontFamilyWithFallback(customPreset.fontFamily)
      : DEFAULT_FONT_FAMILY;
  }
  const found = FONT_PRESETS.find((p) => p.id === normalized);
  return found ? found.stack : DEFAULT_FONT_FAMILY;
}

export function syncCustomFontStylesheets(presets = []) {
  if (typeof document === "undefined" || !document.head) return;
  const desired = new Set(
    presets
      .map((item) => normalizeFontStylesheetUrl(item && item.stylesheetUrl))
      .filter(Boolean)
  );
  const existing = document.head.querySelectorAll('link[data-va-font-css="1"]');
  const present = new Set();
  existing.forEach((link) => {
    const href = link.getAttribute("data-va-font-href") || link.href;
    if (!desired.has(href)) {
      link.remove();
    } else {
      present.add(href);
    }
  });
  desired.forEach((href) => {
    if (present.has(href)) return;
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = href;
    link.setAttribute("data-va-font-css", "1");
    link.setAttribute("data-va-font-href", href);
    document.head.appendChild(link);
  });
}

export function applyAppearanceSettings(settings = getAppearanceSettings()) {
  const root = document.documentElement;
  if (!root) return;

  const customFontPresets = Array.isArray(settings.customFontPresets)
    ? normalizeCustomFontPresets(settings.customFontPresets)
    : loadCustomFontPresets();

  const uiStyle = UI_STYLE_PRESETS.some((s) => s.id === settings.uiStyle)
    ? settings.uiStyle
    : "precision";
  root.setAttribute("data-ui-style", uiStyle);

  const stack = resolveFontFamily(settings.fontPreset, settings.customFont, customFontPresets);
  root.style.setProperty("--va-font-family", stack);
  syncCustomFontStylesheets(customFontPresets);

  if (settings.fontSize !== null && settings.fontSize !== undefined) {
    const clamped = Math.min(
      FONT_SIZE_MAX,
      Math.max(FONT_SIZE_MIN, parseInt(settings.fontSize, 10) || 16)
    );
    root.style.setProperty("--va-font-size", `${clamped}px`);
    root.setAttribute("data-custom-font-size", "true");
  } else {
    root.style.removeProperty("--va-font-size");
    root.removeAttribute("data-custom-font-size");
  }
  const widthPreset =
    LAYOUT_WIDTH_PRESETS.find((w) => w.id === settings.layoutWidth) ||
    LAYOUT_WIDTH_PRESETS[1];
  root.style.setProperty("--va-content-max-width", widthPreset.value);
}

export function saveAppearanceSettings(settings) {
  const customFontPresets = Array.isArray(settings.customFontPresets)
    ? normalizeCustomFontPresets(settings.customFontPresets)
    : loadCustomFontPresets();
  const normalized = {
    uiStyle: UI_STYLE_PRESETS.some((s) => s.id === settings.uiStyle)
      ? settings.uiStyle
      : "precision",
    fontPreset: normalizeFontPreset(settings.fontPreset, customFontPresets),
    customFont: settings.customFont || "",
    customFontPresets,
    fontSize: settings.fontSize ?? null,
    layoutWidth: settings.layoutWidth || "wide",
  };
  try {
    localStorage.setItem(STORAGE_UI_STYLE, normalized.uiStyle);
    localStorage.setItem(STORAGE_FONT_PRESET, normalized.fontPreset);
    localStorage.setItem(STORAGE_FONT_CUSTOM, normalized.customFont);
    localStorage.setItem(
      STORAGE_CUSTOM_FONT_PRESETS,
      JSON.stringify(normalized.customFontPresets)
    );
    localStorage.setItem(STORAGE_LAYOUT_WIDTH, normalized.layoutWidth);
    if (normalized.fontSize === null || normalized.fontSize === undefined) {
      localStorage.removeItem(STORAGE_FONT_SIZE);
    } else {
      localStorage.setItem(STORAGE_FONT_SIZE, String(normalized.fontSize));
    }
  } catch (e) {
    // Ignore storage quota or private-mode errors.
  }
  applyAppearanceSettings(normalized);
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent("va-appearance-change", { detail: normalized }));
  }
}

export function resetAppearanceSettings() {
  const customFontPresets = loadCustomFontPresets();
  try {
    localStorage.removeItem(STORAGE_UI_STYLE);
    localStorage.removeItem(STORAGE_FONT_PRESET);
    localStorage.removeItem(STORAGE_FONT_CUSTOM);
    localStorage.removeItem(STORAGE_FONT_SIZE);
    localStorage.removeItem(STORAGE_LAYOUT_WIDTH);
  } catch (e) {
    // Ignore storage errors.
  }
  const defaults = {
    uiStyle: "precision",
    fontPreset: "system",
    customFont: "",
    customFontPresets,
    fontSize: null,
    layoutWidth: "wide",
  };
  applyAppearanceSettings(defaults);
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent("va-appearance-change", { detail: defaults }));
  }
  return defaults;
}
