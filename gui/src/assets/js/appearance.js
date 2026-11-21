const STORAGE_FONT_PRESET = "va_font_preset";
const STORAGE_FONT_CUSTOM = "va_font_custom";
const STORAGE_FONT_SIZE = "va_font_size";
const STORAGE_LAYOUT_WIDTH = "va_layout_width";
const STORAGE_UI_STYLE = "va_ui_style";

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

function normalizeFontPreset(preset) {
  const mapped = LEGACY_FONT_PRESET_MAP[preset] || preset;
  return FONT_PRESETS.some((p) => p.id === mapped) ? mapped : "system";
}

export const FONT_SIZE_MIN = 12;
export const FONT_SIZE_MAX = 22;

export function getAppearanceSettings() {
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
  const validPreset = normalizeFontPreset(preset);
  const rawWidth = localStorage.getItem(STORAGE_LAYOUT_WIDTH) || "wide";
  const layoutWidth = LAYOUT_WIDTH_PRESETS.some((w) => w.id === rawWidth) ? rawWidth : "wide";
  const rawStyle = localStorage.getItem(STORAGE_UI_STYLE) || "precision";
  const uiStyle = UI_STYLE_PRESETS.some((s) => s.id === rawStyle) ? rawStyle : "precision";
  return {
    uiStyle,
    fontPreset: validPreset,
    customFont,
    fontSize,
    layoutWidth,
  };
}

export function resolveFontFamily(fontPreset, customFont) {
  const normalized = normalizeFontPreset(fontPreset);
  if (normalized === "custom") {
    const trimmed = (customFont || "").trim();
    if (trimmed) {
      return `${trimmed}, ${DEFAULT_FONT_FAMILY}`;
    }
    return DEFAULT_FONT_FAMILY;
  }
  const found = FONT_PRESETS.find((p) => p.id === normalized);
  return found ? found.stack : DEFAULT_FONT_FAMILY;
}

export function applyAppearanceSettings(settings = getAppearanceSettings()) {
  const root = document.documentElement;
  if (!root) return;

  const uiStyle = UI_STYLE_PRESETS.some((s) => s.id === settings.uiStyle)
    ? settings.uiStyle
    : "precision";
  root.setAttribute("data-ui-style", uiStyle);

  const stack = resolveFontFamily(settings.fontPreset, settings.customFont);
  root.style.setProperty("--va-font-family", stack);

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
  const normalized = {
    uiStyle: UI_STYLE_PRESETS.some((s) => s.id === settings.uiStyle)
      ? settings.uiStyle
      : "precision",
    fontPreset: normalizeFontPreset(settings.fontPreset),
    customFont: settings.customFont || "",
    fontSize: settings.fontSize ?? null,
    layoutWidth: settings.layoutWidth || "wide",
  };
  localStorage.setItem(STORAGE_UI_STYLE, normalized.uiStyle);
  localStorage.setItem(STORAGE_FONT_PRESET, normalized.fontPreset);
  localStorage.setItem(STORAGE_FONT_CUSTOM, normalized.customFont);
  localStorage.setItem(STORAGE_LAYOUT_WIDTH, normalized.layoutWidth);
  if (normalized.fontSize === null || normalized.fontSize === undefined) {
    localStorage.removeItem(STORAGE_FONT_SIZE);
  } else {
    localStorage.setItem(STORAGE_FONT_SIZE, String(normalized.fontSize));
  }
  applyAppearanceSettings(normalized);
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent("va-appearance-change", { detail: normalized }));
  }
}

export function resetAppearanceSettings() {
  localStorage.removeItem(STORAGE_UI_STYLE);
  localStorage.removeItem(STORAGE_FONT_PRESET);
  localStorage.removeItem(STORAGE_FONT_CUSTOM);
  localStorage.removeItem(STORAGE_FONT_SIZE);
  localStorage.removeItem(STORAGE_LAYOUT_WIDTH);
  const defaults = {
    uiStyle: "precision",
    fontPreset: "system",
    customFont: "",
    fontSize: null,
    layoutWidth: "wide",
  };
  applyAppearanceSettings(defaults);
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent("va-appearance-change", { detail: defaults }));
  }
  return defaults;
}
