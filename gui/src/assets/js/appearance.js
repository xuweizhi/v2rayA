const STORAGE_FONT_PRESET = "va_font_preset";
const STORAGE_FONT_CUSTOM = "va_font_custom";
const STORAGE_FONT_SIZE = "va_font_size";
const STORAGE_LAYOUT_WIDTH = "va_layout_width";

export const LAYOUT_WIDTH_PRESETS = [
  { id: "compact", labelKey: "appearance.widths.compact", value: "1280px" },
  { id: "wide", labelKey: "appearance.widths.wide", value: "1680px" },
  { id: "full", labelKey: "appearance.widths.full", value: "100%" },
];

const SYSTEM_SANS =
  'BlinkMacSystemFont, -apple-system, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans CJK SC", Roboto, Helvetica, Arial, sans-serif';

export const FONT_PRESETS = [
  {
    id: "system",
    labelKey: "appearance.fonts.system",
    stack: SYSTEM_SANS,
  },
  {
    id: "inter",
    labelKey: "appearance.fonts.inter",
    stack: `"sarasa inter regular", "PingFang SC", "Microsoft YaHei", ${SYSTEM_SANS}`,
  },
  {
    id: "sans-cn",
    labelKey: "appearance.fonts.sansCn",
    stack:
      '"PingFang SC", "Microsoft YaHei", "Noto Sans SC", "Source Han Sans SC", "WenQuanYi Micro Hei", sans-serif',
  },
  {
    id: "serif",
    labelKey: "appearance.fonts.serif",
    stack:
      '"Songti SC", "Noto Serif SC", "Source Han Serif SC", "STSong", Georgia, "Times New Roman", serif',
  },
  {
    id: "mono",
    labelKey: "appearance.fonts.mono",
    stack:
      '"JetBrains Mono", "Cascadia Code", "Fira Code", "SFMono-Regular", Consolas, Menlo, Monaco, monospace',
  },
  {
    id: "custom",
    labelKey: "appearance.fonts.custom",
    stack: SYSTEM_SANS,
  },
];

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
  const validPreset = FONT_PRESETS.some((p) => p.id === preset) ? preset : "system";
  const rawWidth = localStorage.getItem(STORAGE_LAYOUT_WIDTH) || "wide";
  const layoutWidth = LAYOUT_WIDTH_PRESETS.some((w) => w.id === rawWidth) ? rawWidth : "wide";
  return {
    fontPreset: validPreset,
    customFont,
    fontSize,
    layoutWidth,
  };
}

export function resolveFontFamily(fontPreset, customFont) {
  if (fontPreset === "custom") {
    const trimmed = (customFont || "").trim();
    if (trimmed) {
      return `${trimmed}, ${SYSTEM_SANS}`;
    }
    return SYSTEM_SANS;
  }
  const found = FONT_PRESETS.find((p) => p.id === fontPreset);
  return found ? found.stack : SYSTEM_SANS;
}

export function applyAppearanceSettings(settings = getAppearanceSettings()) {
  const root = document.documentElement;
  if (!root) return;

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
  localStorage.setItem(STORAGE_FONT_PRESET, settings.fontPreset || "system");
  localStorage.setItem(STORAGE_FONT_CUSTOM, settings.customFont || "");
  localStorage.setItem(STORAGE_LAYOUT_WIDTH, settings.layoutWidth || "wide");
  if (settings.fontSize === null || settings.fontSize === undefined) {
    localStorage.removeItem(STORAGE_FONT_SIZE);
  } else {
    localStorage.setItem(STORAGE_FONT_SIZE, String(settings.fontSize));
  }
  applyAppearanceSettings(settings);
}

export function resetAppearanceSettings() {
  localStorage.removeItem(STORAGE_FONT_PRESET);
  localStorage.removeItem(STORAGE_FONT_CUSTOM);
  localStorage.removeItem(STORAGE_FONT_SIZE);
  localStorage.removeItem(STORAGE_LAYOUT_WIDTH);
  const defaults = { fontPreset: "system", customFont: "", fontSize: null, layoutWidth: "wide" };
  applyAppearanceSettings(defaults);
  return defaults;
}
