<template>
  <div class="appearance-panel">
    <div class="appearance-section">
      <div class="appearance-section__head">
        <label class="label">{{ $t("appearance.uiStyle") }}</label>
        <span class="appearance-instant-hint">{{ $t("appearance.instantHint") }}</span>
      </div>
      <div class="style-card-grid" role="radiogroup" :aria-label="$t('appearance.uiStyle')">
        <button
          v-for="style in uiStylePresets"
          :key="style.id"
          type="button"
          class="style-card"
          :class="[`style-card--${style.id}`, { 'is-selected': uiStyle === style.id }]"
          role="radio"
          :aria-checked="String(uiStyle === style.id)"
          @click="selectUiStyle(style.id)"
        >
          <div class="style-mini" :class="`style-mini--${style.id}`" aria-hidden="true">
            <div class="style-mini__bar">
              <span class="style-mini__dot"></span>
              <span class="style-mini__pill"></span>
            </div>
            <div class="style-mini__body">
              <div class="style-mini__card">
                <span class="style-mini__line is-short"></span>
                <span class="style-mini__line is-long"></span>
              </div>
              <div class="style-mini__cta"></div>
            </div>
          </div>
          <div class="style-card__meta">
            <div class="style-card__title-row">
              <span class="style-card__badge">{{ style.badge }}</span>
              <span class="style-card__name">{{ $t(style.labelKey) }}</span>
              <i
                v-if="uiStyle === style.id"
                class="mdi mdi-check-circle style-card__check"
                aria-hidden="true"
              ></i>
            </div>
            <p class="style-card__desc">{{ $t(style.descKey) }}</p>
          </div>
        </button>
      </div>
    </div>

    <div class="appearance-section appearance-row-section">
      <label class="label">{{ $t("appearance.colorMode") }}</label>
      <div class="va-chip-group" role="radiogroup" :aria-label="$t('appearance.colorMode')">
        <button
          v-for="mode in colorModes"
          :key="mode.id"
          type="button"
          class="va-chip"
          :class="{ 'is-selected': colorMode === mode.id }"
          role="radio"
          :aria-checked="String(colorMode === mode.id)"
          @click="selectColorMode(mode.id)"
        >
          <i :class="`mdi ${mode.icon}`" aria-hidden="true"></i>
          {{ $t(mode.labelKey) }}
        </button>
      </div>
    </div>

    <hr class="appearance-divider" />

    <div class="appearance-section">
      <div class="appearance-section__head">
        <label class="label">{{ $t("appearance.fontSize") }}</label>
        <span class="appearance-size-value">
          {{
            fontSize === null
              ? $t("appearance.sizeAuto")
              : `${fontSize}px (${Math.round((fontSize / 16) * 100)}%)`
          }}
        </span>
      </div>

      <div class="va-chip-group appearance-size-presets" role="group" :aria-label="$t('appearance.fontSize')">
        <button
          v-for="opt in sizePresets"
          :key="String(opt.value)"
          type="button"
          class="va-chip"
          :class="{ 'is-selected': fontSize === opt.value }"
          :aria-pressed="String(fontSize === opt.value)"
          @click="selectFontSize(opt.value)"
        >
          {{ $t(opt.labelKey) }}
        </button>
      </div>

      <div class="appearance-slider-row">
        <span class="appearance-slider-edge">A</span>
        <input
          type="range"
          class="appearance-slider"
          :min="minSize"
          :max="maxSize"
          step="1"
          :value="sliderValue"
          :aria-label="$t('appearance.fontSize')"
          @input="selectFontSize(Number($event.target.value))"
        />
        <span class="appearance-slider-edge is-large">A</span>
      </div>
    </div>

    <hr class="appearance-divider" />

    <div class="appearance-section">
      <label class="label">{{ $t("appearance.fontFamily") }}</label>
      <div class="appearance-font-grid" role="group" :aria-label="$t('appearance.fontFamily')">
        <button
          v-for="preset in builtinFontPresets"
          :key="preset.id"
          type="button"
          class="appearance-font-card"
          :class="{ 'is-selected': fontPreset === preset.id }"
          :aria-pressed="String(fontPreset === preset.id)"
          @click="selectFontPreset(preset.id)"
        >
          <span
            class="appearance-font-card__sample"
            :style="{ fontFamily: presetSampleFont(preset) }"
          >
            Aa 节点
          </span>
          <span
            class="appearance-font-card__name"
            :style="{ fontFamily: preset.stack }"
          >
            {{ $t(preset.labelKey) }}
          </span>
        </button>

        <div
          v-for="preset in customFontPresets"
          :key="preset.id"
          class="appearance-font-card-wrap"
          :class="{ 'is-selected': fontPreset === presetKey(preset.id) }"
        >
          <button
            type="button"
            class="appearance-font-card appearance-font-card--custom-preset"
            :class="{ 'is-selected': fontPreset === presetKey(preset.id) }"
            :aria-pressed="String(fontPreset === presetKey(preset.id))"
            :title="presetCardTitle(preset)"
            @click="selectFontPreset(presetKey(preset.id))"
          >
            <span
              class="appearance-font-card__sample"
              :style="{ fontFamily: customPresetSampleFont(preset) }"
            >
              Aa 节点
            </span>
            <span class="appearance-font-card__name-row">
              <span
                class="appearance-font-card__name"
                :style="{ fontFamily: customPresetSampleFont(preset) }"
              >
                {{ preset.label }}
              </span>
              <span
                v-if="preset.stylesheetUrl"
                class="appearance-font-card__web-dot"
                :title="$t('appearance.webFontTag')"
                aria-hidden="true"
              ></span>
            </span>
          </button>
          <button
            type="button"
            class="appearance-font-card__remove"
            :aria-label="$t('appearance.deletePresetAria', { name: preset.label })"
            :title="$t('appearance.deletePresetAria', { name: preset.label })"
            @click="removeCustomPreset(preset)"
          >
            ×
          </button>
        </div>

        <button
          v-if="customFontOption"
          type="button"
          class="appearance-font-card"
          :class="{ 'is-selected': fontPreset === 'custom' }"
          :aria-pressed="String(fontPreset === 'custom')"
          @click="selectFontPreset('custom')"
        >
          <span
            class="appearance-font-card__sample"
            :style="{ fontFamily: presetSampleFont(customFontOption) }"
          >
            Aa 节点
          </span>
          <span class="appearance-font-card__name">
            {{ $t(customFontOption.labelKey) }}
          </span>
        </button>

        <button
          type="button"
          class="appearance-font-card appearance-font-card--add"
          :class="{ 'is-selected': creatorOpen }"
          :aria-expanded="String(creatorOpen)"
          :disabled="atPresetLimit && !creatorOpen"
          :title="
            atPresetLimit
              ? $t('appearance.addPresetLimitTitle', { max: maxCustomFontPresets })
              : $t('appearance.addPresetTitle')
          "
          @click="toggleCreator"
        >
          <span class="appearance-font-card__sample" aria-hidden="true">＋</span>
          <span class="appearance-font-card__name">{{ $t("appearance.addPreset") }}</span>
        </button>
      </div>

      <div v-if="fontPreset === 'custom'" class="appearance-custom-font">
        <label class="appearance-custom-font__label" for="va-custom-font-input">
          {{ $t("appearance.customFontLabel") }}
        </label>
        <div class="appearance-custom-font__row">
          <input
            id="va-custom-font-input"
            v-model="customFont"
            type="text"
            class="input is-small"
            :aria-label="$t('appearance.customFontLabel')"
            :placeholder="$t('appearance.customFontPlaceholder')"
            @input="persist"
          />
          <button
            v-if="!atPresetLimit && customFont.trim()"
            type="button"
            class="button is-small is-primary is-outlined"
            @click="openCreator(customFont.trim())"
          >
            {{ $t("appearance.saveAsPreset") }}
          </button>
          <button
            type="button"
            class="button is-small"
            @click="resetFontToDefault"
          >
            {{ $t("appearance.resetFont") }}
          </button>
        </div>
        <p class="appearance-custom-font__help">{{ $t("appearance.customFontHelp") }}</p>
      </div>

      <form
        v-if="creatorOpen"
        class="appearance-font-creator"
        @submit.prevent="submitPreset"
      >
        <div class="appearance-font-creator__head">
          <strong>{{ $t("appearance.presetCreatorTitle") }}</strong>
          <small>{{ $t("appearance.presetCreatorHint") }}</small>
        </div>
        <div class="appearance-font-creator__fields">
          <label class="appearance-font-creator__field">
            <span>{{ $t("appearance.presetNameLabel") }}</span>
            <input
              v-model="draftLabel"
              type="text"
              class="input is-small"
              maxlength="32"
              :aria-label="$t('appearance.presetNameAria')"
              :placeholder="$t('appearance.presetNamePlaceholder')"
              @input="draftError = ''"
            />
          </label>
          <label class="appearance-font-creator__field">
            <span>{{ $t("appearance.presetFamilyLabel") }}</span>
            <input
              v-model="draftFamily"
              type="text"
              class="input is-small"
              maxlength="240"
              :aria-label="$t('appearance.presetFamilyAria')"
              :placeholder="$t('appearance.presetFamilyPlaceholder')"
              @input="draftError = ''"
            />
          </label>
          <label class="appearance-font-creator__field">
            <span>{{ $t("appearance.presetUrlLabel") }}</span>
            <input
              v-model="draftUrl"
              type="text"
              class="input is-small"
              :aria-label="$t('appearance.presetUrlAria')"
              :placeholder="$t('appearance.presetUrlPlaceholder')"
              @input="draftError = ''"
            />
          </label>
        </div>
        <div
          class="appearance-font-creator__preview"
          :style="{ fontFamily: creatorPreviewFont }"
        >
          <span>{{ $t("appearance.previewTitle") }}</span>
          <p>{{ $t("appearance.presetPreviewSample") }}</p>
        </div>
        <div
          v-if="draftError"
          class="appearance-font-creator__error"
          role="alert"
        >
          {{ draftError }}
        </div>
        <div class="appearance-font-creator__actions">
          <button type="button" class="button is-small" @click="closeCreator">
            {{ $t("operations.cancel") }}
          </button>
          <button type="submit" class="button is-small is-primary">
            {{ $t("appearance.presetSaveApply") }}
          </button>
        </div>
      </form>
    </div>

    <hr class="appearance-divider" />

    <div class="appearance-section">
      <label class="label">{{ $t("appearance.layoutWidth") }}</label>
      <div class="va-chip-group" role="radiogroup" :aria-label="$t('appearance.layoutWidth')">
        <button
          v-for="w in layoutWidthPresets"
          :key="w.id"
          type="button"
          class="va-chip"
          :class="{ 'is-selected': layoutWidth === w.id }"
          role="radio"
          :aria-checked="String(layoutWidth === w.id)"
          @click="selectLayoutWidth(w.id)"
        >
          {{ $t(w.labelKey) }}
        </button>
      </div>
    </div>

    <div class="appearance-preview">
      <div class="appearance-preview__head">
        <span class="appearance-preview__label">{{ $t("appearance.previewTitle") }}</span>
        <span class="appearance-preview__badges">
          <span class="preview-proto">VLESS</span>
          <span class="preview-latency">
            <span class="preview-latency__dot" aria-hidden="true"></span>
            42 ms
          </span>
        </span>
      </div>
      <div class="appearance-preview__row">
        <div class="appearance-preview__info">
          <strong class="appearance-preview__title">{{ $t("appearance.previewSampleTitle") }}</strong>
          <code class="appearance-preview__code code-font">vless.example.com:443 · TLS · XHTTP</code>
        </div>
        <button type="button" class="button is-small is-primary" tabindex="-1">
          {{ $t("operations.connect") }}
        </button>
      </div>
      <p class="appearance-preview__desc">{{ $t("appearance.previewSampleDesc") }}</p>
    </div>

    <div v-if="showReset" class="appearance-inline-reset">
      <b-button size="is-small" icon-left="restore" @click="handleReset">
        {{ $t("appearance.reset") }}
      </b-button>
    </div>
  </div>
</template>

<script>
import {
  FONT_PRESETS,
  FONT_SIZE_MAX,
  FONT_SIZE_MIN,
  LAYOUT_WIDTH_PRESETS,
  MAX_CUSTOM_FONT_PRESETS,
  UI_STYLE_PRESETS,
  createCustomFontPresetId,
  customFontPresetKey,
  formatFontFamilyWithFallback,
  getAppearanceSettings,
  normalizeFontStylesheetUrl,
  resetAppearanceSettings,
  resolveFontFamily,
  saveAppearanceSettings,
  suggestFontPresetLabel,
} from "@/assets/js/appearance";

export default {
  name: "AppearancePanel",
  props: {
    showReset: {
      type: Boolean,
      default: false,
    },
  },
  data() {
    const initial = getAppearanceSettings();
    const storedTheme = localStorage.getItem("theme");
    return {
      uiStyle: initial.uiStyle,
      colorMode: storedTheme === "dark" || storedTheme === "light" ? storedTheme : "auto",
      fontPreset: initial.fontPreset,
      customFont: initial.customFont,
      customFontPresets: initial.customFontPresets,
      fontSize: initial.fontSize,
      layoutWidth: initial.layoutWidth,
      creatorOpen: false,
      draftLabel: "",
      draftFamily: "",
      draftUrl: "",
      draftError: "",
      uiStylePresets: UI_STYLE_PRESETS,
      fontPresets: FONT_PRESETS,
      layoutWidthPresets: LAYOUT_WIDTH_PRESETS,
      maxCustomFontPresets: MAX_CUSTOM_FONT_PRESETS,
      minSize: FONT_SIZE_MIN,
      maxSize: FONT_SIZE_MAX,
      colorModes: [
        { id: "auto", labelKey: "common.autoTheme", icon: "mdi-theme-light-dark" },
        { id: "light", labelKey: "common.lightTheme", icon: "mdi-weather-sunny" },
        { id: "dark", labelKey: "common.darkTheme", icon: "mdi-weather-night" },
      ],
      sizePresets: [
        { value: null, labelKey: "appearance.sizeAuto" },
        { value: 14, labelKey: "appearance.sizeSmall" },
        { value: 16, labelKey: "appearance.sizeNormal" },
        { value: 18, labelKey: "appearance.sizeLarge" },
        { value: 20, labelKey: "appearance.sizeXLarge" },
      ],
    };
  },
  computed: {
    sliderValue() {
      return this.fontSize === null ? 16 : this.fontSize;
    },
    builtinFontPresets() {
      return this.fontPresets.filter((preset) => preset.id !== "custom");
    },
    customFontOption() {
      return this.fontPresets.find((preset) => preset.id === "custom") || null;
    },
    atPresetLimit() {
      return this.customFontPresets.length >= this.maxCustomFontPresets;
    },
    creatorPreviewFont() {
      if (this.draftFamily.trim()) {
        return formatFontFamilyWithFallback(this.draftFamily);
      }
      return resolveFontFamily(this.fontPreset, this.customFont, this.customFontPresets);
    },
  },
  mounted() {
    this._onExternalAppearance = (e) => {
      const next = (e && e.detail) || getAppearanceSettings();
      this.uiStyle = next.uiStyle;
      this.fontPreset = next.fontPreset;
      this.customFont = next.customFont;
      this.customFontPresets = Array.isArray(next.customFontPresets)
        ? next.customFontPresets
        : [];
      this.fontSize = next.fontSize;
      this.layoutWidth = next.layoutWidth;
    };
    this._onExternalTheme = (e) => {
      const pref = e && e.detail;
      if (pref === "auto" || pref === "light" || pref === "dark") {
        this.colorMode = pref;
      }
    };
    window.addEventListener("va-appearance-change", this._onExternalAppearance);
    window.addEventListener("va-theme-sync", this._onExternalTheme);
  },
  beforeDestroy() {
    if (this._onExternalAppearance) {
      window.removeEventListener("va-appearance-change", this._onExternalAppearance);
    }
    if (this._onExternalTheme) {
      window.removeEventListener("va-theme-sync", this._onExternalTheme);
    }
  },
  methods: {
    persist() {
      saveAppearanceSettings({
        uiStyle: this.uiStyle,
        fontPreset: this.fontPreset,
        customFont: this.customFont,
        customFontPresets: this.customFontPresets,
        fontSize: this.fontSize,
        layoutWidth: this.layoutWidth,
      });
    },
    selectUiStyle(id) {
      this.uiStyle = id;
      this.persist();
    },
    selectColorMode(mode) {
      this.colorMode = mode;
      window.dispatchEvent(new CustomEvent("va-theme-change", { detail: mode }));
    },
    selectFontSize(val) {
      this.fontSize = val;
      this.persist();
    },
    selectFontPreset(id) {
      this.fontPreset = id;
      this.persist();
    },
    presetKey(id) {
      return customFontPresetKey(id);
    },
    presetCardTitle(preset) {
      if (preset.stylesheetUrl) {
        return `${preset.fontFamily} · ${this.$t("appearance.webFontTag")}`;
      }
      return preset.fontFamily;
    },
    customPresetSampleFont(preset) {
      return formatFontFamilyWithFallback(preset.fontFamily);
    },
    presetSampleFont(preset) {
      if (preset.id === "custom") {
        return resolveFontFamily("custom", this.customFont, this.customFontPresets);
      }
      return preset.stack;
    },
    openCreator(seedFamily = "") {
      this.draftError = "";
      this.draftFamily = seedFamily;
      this.draftLabel = seedFamily ? suggestFontPresetLabel(seedFamily) : "";
      this.draftUrl = "";
      this.creatorOpen = true;
    },
    closeCreator() {
      this.creatorOpen = false;
      this.draftError = "";
    },
    toggleCreator() {
      if (this.creatorOpen) {
        this.closeCreator();
      } else {
        this.openCreator();
      }
    },
    submitPreset() {
      const label = this.draftLabel.trim().slice(0, 32);
      const fontFamily = this.draftFamily.trim().slice(0, 240);
      const rawUrl = this.draftUrl.trim();

      if (!label) {
        this.draftError = this.$t("appearance.presetErrors.emptyName");
        return;
      }
      if (!fontFamily) {
        this.draftError = this.$t("appearance.presetErrors.emptyFamily");
        return;
      }
      if (this.atPresetLimit) {
        this.draftError = this.$t("appearance.presetErrors.limitReached", {
          max: this.maxCustomFontPresets,
        });
        return;
      }
      const duplicate =
        this.builtinFontPresets.some((opt) => opt.stack === fontFamily) ||
        this.customFontPresets.some((opt) => opt.fontFamily === fontFamily);
      if (duplicate) {
        this.draftError = this.$t("appearance.presetErrors.duplicate");
        return;
      }
      const stylesheetUrl = rawUrl ? normalizeFontStylesheetUrl(rawUrl) : undefined;
      if (rawUrl && !stylesheetUrl) {
        this.draftError = this.$t("appearance.presetErrors.invalidUrl");
        return;
      }

      const id = createCustomFontPresetId(this.customFontPresets);
      const nextPreset = stylesheetUrl
        ? { id, label, fontFamily, stylesheetUrl }
        : { id, label, fontFamily };

      this.customFontPresets = [...this.customFontPresets, nextPreset];
      this.fontPreset = customFontPresetKey(nextPreset.id);
      this.persist();

      this.draftLabel = "";
      this.draftFamily = "";
      this.draftUrl = "";
      this.draftError = "";
      this.creatorOpen = false;
    },
    removeCustomPreset(preset) {
      this.customFontPresets = this.customFontPresets.filter((item) => item.id !== preset.id);
      if (this.fontPreset === customFontPresetKey(preset.id)) {
        this.fontPreset = "system";
      }
      this.persist();
    },
    resetFontToDefault() {
      this.fontPreset = "system";
      this.customFont = "";
      this.persist();
    },
    selectLayoutWidth(id) {
      this.layoutWidth = id;
      this.persist();
    },
    handleReset() {
      const defaults = resetAppearanceSettings();
      this.uiStyle = defaults.uiStyle;
      this.fontPreset = defaults.fontPreset;
      this.customFont = defaults.customFont;
      this.customFontPresets = defaults.customFontPresets;
      this.fontSize = defaults.fontSize;
      this.layoutWidth = defaults.layoutWidth;
      this.selectColorMode("auto");
      this.closeCreator();
      this.$buefy.toast.open({
        message: this.$t("appearance.resetSuccess"),
        type: "is-success",
        position: "is-top",
        duration: 2000,
        queue: false,
      });
    },
  },
};
</script>

<style lang="scss" scoped>
.appearance-section__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 0.5rem;
  margin-bottom: 0.6rem;

  .label {
    margin-bottom: 0;
  }
}

.appearance-instant-hint {
  font-size: 0.75rem;
  color: var(--va-text-subtle);
}

.appearance-row-section {
  margin-top: 1rem;
}

.style-card-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.65rem;
}

.style-card {
  appearance: none;
  display: flex;
  flex-direction: column;
  padding: 0;
  overflow: hidden;
  border-radius: var(--va-radius-md);
  border: 1px solid var(--va-border);
  background: var(--va-surface-subtle);
  color: var(--va-text);
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition: transform 0.14s ease, border-color 0.16s ease, box-shadow 0.18s ease;

  &:hover {
    border-color: var(--va-primary);
    transform: translateY(-1px);
    box-shadow: var(--va-shadow-sm);
  }

  &.is-selected {
    border-color: var(--va-primary);
    box-shadow: 0 0 0 2px var(--va-focus-ring), var(--va-shadow-sm);
  }
}

.style-mini {
  height: 58px;
  padding: 7px 8px;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  border-bottom: 1px solid var(--va-border);
}

.style-mini__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.style-mini__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
}

.style-mini__pill {
  width: 28px;
  height: 6px;
  border-radius: 999px;
}

.style-mini__body {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}

.style-mini__card {
  flex: 1;
  padding: 5px 6px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.style-mini__line {
  display: block;
  height: 4px;
  border-radius: 999px;

  &.is-short {
    width: 55%;
  }

  &.is-long {
    width: 85%;
    opacity: 0.55;
  }
}

.style-mini__cta {
  width: 24px;
  height: 16px;
  flex-shrink: 0;
}

/* Miniature swatches for each style */
.style-mini--precision {
  background: radial-gradient(circle at 80% 0%, rgba(59, 108, 245, 0.22), transparent 60%), #eef2fa;

  .style-mini__dot {
    background: #10b981;
  }

  .style-mini__pill {
    background: rgba(59, 108, 245, 0.25);
  }

  .style-mini__card {
    background: #ffffff;
    border: 1px solid #d9e2f2;
    border-radius: 6px;
  }

  .style-mini__line {
    background: #3b4b6b;
  }

  .style-mini__cta {
    border-radius: 5px;
    background: linear-gradient(180deg, #5581ff 0%, #3360eb 100%);
    box-shadow: 0 2px 6px rgba(59, 108, 245, 0.35);
  }
}

.style-mini--soft {
  background: #f5f2eb;

  .style-mini__dot {
    background: #2f8f6b;
  }

  .style-mini__pill {
    background: rgba(79, 114, 143, 0.22);
  }

  .style-mini__card {
    background: #fdfcfa;
    border: 1px solid #e5e0d5;
    border-radius: 10px;
  }

  .style-mini__line {
    background: #5b5f66;
  }

  .style-mini__cta {
    border-radius: 999px;
    background: #4f728f;
  }
}

.style-mini--cyber {
  background:
    radial-gradient(rgba(56, 189, 248, 0.28) 1px, transparent 1px),
    radial-gradient(circle at 80% 0%, rgba(34, 211, 238, 0.28), transparent 60%),
    #080c16;
  background-size: 9px 9px, 100% 100%, 100% 100%;

  .style-mini__dot {
    background: #10f0a0;
    box-shadow: 0 0 6px #10f0a0;
  }

  .style-mini__pill {
    background: rgba(34, 211, 238, 0.35);
  }

  .style-mini__card {
    background: rgba(15, 22, 39, 0.92);
    border: 1px solid rgba(56, 189, 248, 0.35);
    border-radius: 4px;
  }

  .style-mini__line {
    background: #93c5fd;
  }

  .style-mini__cta {
    border-radius: 4px;
    background: linear-gradient(180deg, #06b6d4 0%, #0284c7 100%);
    box-shadow: 0 0 8px rgba(34, 211, 238, 0.65);
  }
}

.style-card__meta {
  padding: 0.55rem 0.65rem 0.65rem;
  display: flex;
  flex-direction: column;
  gap: 0.24rem;
}

.style-card__title-row {
  display: flex;
  align-items: center;
  gap: 0.35rem;
}

.style-card__badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.15rem;
  height: 1.15rem;
  border-radius: 999px;
  font-size: 0.68rem;
  font-weight: 800;
  background: var(--va-primary-soft);
  color: var(--va-primary);
  flex-shrink: 0;
}

.style-card__name {
  font-size: 0.84rem;
  font-weight: 700;
  color: var(--va-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.style-card__check {
  margin-left: auto;
  color: var(--va-primary);
  font-size: 1rem;
  flex-shrink: 0;
}

.style-card__desc {
  margin: 0;
  font-size: 0.72rem;
  line-height: 1.4;
  color: var(--va-text-muted);
}

.appearance-size-value {
  font-size: 0.84rem;
  font-weight: 600;
  color: var(--va-primary);
  font-variant-numeric: tabular-nums;
}

.appearance-size-presets {
  margin-bottom: 0.8rem;
}

.appearance-slider-row {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.appearance-slider {
  flex: 1;
  accent-color: var(--va-primary);
  cursor: pointer;
}

.appearance-slider-edge {
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--va-text-muted);
  user-select: none;

  &.is-large {
    font-size: 1.25rem;
  }
}

.appearance-divider {
  margin: 1.05rem 0;
}

.appearance-font-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.55rem;
}

.appearance-font-card-wrap {
  position: relative;
  display: flex;
  min-width: 0;
}

.appearance-font-card {
  appearance: none;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 0.22rem;
  width: 100%;
  min-width: 0;
  padding: 0.62rem 0.72rem;
  border-radius: var(--va-radius-sm);
  border: 1px solid var(--va-border);
  background: var(--va-surface-subtle);
  color: var(--va-text);
  font: inherit;
  cursor: pointer;
  text-align: left;
  transition: border-color 0.15s ease, background-color 0.15s ease, color 0.15s ease;

  &:hover:not(:disabled) {
    border-color: var(--va-primary);
    background: var(--va-primary-soft);
  }

  &.is-selected {
    border-color: var(--va-primary);
    background: var(--va-primary-soft);
    box-shadow: inset 0 0 0 1px var(--va-primary);
  }

  &:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }
}

.appearance-font-card--custom-preset {
  padding-right: 1.85rem;
}

.appearance-font-card--add {
  border-style: dashed;
  border-color: var(--va-border-strong);

  .appearance-font-card__sample,
  .appearance-font-card__name {
    color: var(--va-primary);
  }
}

.appearance-font-card__sample {
  font-size: 1.02rem;
  font-weight: 600;
  color: var(--va-text);
  line-height: 1.3;
}

.appearance-font-card__name-row {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  width: 100%;
  min-width: 0;
}

.appearance-font-card__name {
  font-size: 0.75rem;
  color: var(--va-text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.appearance-font-card__web-dot {
  width: 6px;
  height: 6px;
  border-radius: 999px;
  background: var(--va-primary);
  flex-shrink: 0;
  opacity: 0.85;
}

.appearance-font-card__remove {
  position: absolute;
  top: 0.3rem;
  right: 0.3rem;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.35rem;
  height: 1.35rem;
  padding: 0;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: var(--va-text-subtle);
  font-size: 0.95rem;
  line-height: 1;
  cursor: pointer;
  transition: background-color 0.14s ease, color 0.14s ease;

  &:hover,
  &:focus-visible {
    background: var(--va-danger-soft);
    color: var(--va-danger-text);
  }
}

.appearance-custom-font {
  margin-top: 0.85rem;
  display: flex;
  flex-direction: column;
  gap: 0.42rem;
}

.appearance-custom-font__label {
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--va-text);
}

.appearance-custom-font__row {
  display: flex;
  align-items: center;
  gap: 0.45rem;

  .input {
    flex: 1;
    min-width: 0;
  }

  .button {
    flex-shrink: 0;
  }
}

.appearance-custom-font__help {
  margin: 0;
  font-size: 0.75rem;
  color: var(--va-text-muted);
}

.appearance-font-creator {
  margin-top: 0.85rem;
  padding: 0.85rem 0.95rem;
  border-radius: var(--va-radius-md);
  border: 1px solid var(--va-border);
  background: var(--va-surface-subtle);
  display: flex;
  flex-direction: column;
  gap: 0.72rem;
}

.appearance-font-creator__head {
  display: flex;
  flex-direction: column;
  gap: 0.16rem;

  strong {
    font-size: 0.86rem;
    color: var(--va-text);
  }

  small {
    font-size: 0.74rem;
    color: var(--va-text-muted);
  }
}

.appearance-font-creator__fields {
  display: flex;
  flex-direction: column;
  gap: 0.58rem;
}

.appearance-font-creator__field {
  display: flex;
  flex-direction: column;
  gap: 0.28rem;

  > span {
    font-size: 0.76rem;
    font-weight: 600;
    color: var(--va-text-muted);
  }

  .input {
    background-color: var(--va-surface);
  }
}

.appearance-font-creator__preview {
  padding: 0.6rem 0.75rem;
  border-radius: var(--va-radius-sm);
  border: 1px dashed var(--va-border-strong);
  background: var(--va-surface);
  display: flex;
  flex-direction: column;
  gap: 0.22rem;

  span {
    font-family: var(--va-font-family);
    font-size: 0.7rem;
    font-weight: 700;
    letter-spacing: 0.03em;
    color: var(--va-text-subtle);
  }

  p {
    margin: 0;
    font-size: 0.92rem;
    color: var(--va-text);
    overflow-wrap: anywhere;
  }
}

.appearance-font-creator__error {
  padding: 0.48rem 0.68rem;
  border-radius: var(--va-radius-sm);
  border: 1px solid var(--va-danger);
  background: var(--va-danger-soft);
  color: var(--va-danger-text);
  font-size: 0.78rem;
  font-weight: 600;
}

.appearance-font-creator__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}

.appearance-preview {
  margin-top: 1.05rem;
  padding: 0.85rem 1rem;
  border-radius: var(--va-radius-md);
  border: 1px solid var(--va-border);
  background: var(--va-surface-subtle);
  box-shadow: var(--va-shadow-xs);
}

.appearance-preview__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  margin-bottom: 0.55rem;
}

.appearance-preview__label {
  font-size: 0.72rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--va-text-subtle);
}

.appearance-preview__badges {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
}

.preview-proto {
  display: inline-flex;
  align-items: center;
  padding: 0.12rem 0.48rem;
  border-radius: 6px;
  font-size: 0.7rem;
  font-weight: 700;
  background: var(--va-proto-v2-bg);
  color: var(--va-proto-v2-fg);
}

.preview-latency {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  padding: 0.12rem 0.48rem;
  border-radius: 999px;
  font-size: 0.74rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  background: var(--va-latency-good-bg);
  color: var(--va-latency-good);
}

.preview-latency__dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.appearance-preview__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  margin-bottom: 0.35rem;
}

.appearance-preview__info {
  display: flex;
  flex-direction: column;
  gap: 0.18rem;
  min-width: 0;
}

.appearance-preview__title {
  color: var(--va-text);
  font-size: 0.94rem;
}

.appearance-preview__code {
  font-size: 0.78rem;
  width: fit-content;
}

.appearance-preview__desc {
  font-size: 0.82rem;
  color: var(--va-text-muted);
  margin: 0.25rem 0 0;
}

.appearance-inline-reset {
  margin-top: 0.9rem;
  display: flex;
  justify-content: flex-end;
}

@media screen and (max-width: 560px) {
  .style-card-grid {
    grid-template-columns: 1fr;
  }

  .appearance-font-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .appearance-custom-font__row {
    flex-wrap: wrap;
  }
}
</style>
