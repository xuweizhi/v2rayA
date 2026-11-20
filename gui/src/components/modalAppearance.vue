<template>
  <div class="modal-card appearance-modal">
    <header class="modal-card-head">
      <p class="modal-card-title">
        <i class="mdi mdi-format-font" style="margin-right: 0.4rem" aria-hidden="true"></i>
        {{ $t("appearance.title") }}
      </p>
      <button
        type="button"
        class="delete"
        :aria-label="$t('operations.close')"
        @click="$parent.close()"
      ></button>
    </header>

    <section class="modal-card-body">
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
        <div class="appearance-font-grid" role="radiogroup" :aria-label="$t('appearance.fontFamily')">
          <button
            v-for="preset in fontPresets"
            :key="preset.id"
            type="button"
            class="appearance-font-card"
            :class="{ 'is-selected': fontPreset === preset.id }"
            role="radio"
            :aria-checked="String(fontPreset === preset.id)"
            @click="selectFontPreset(preset.id)"
          >
            <span class="appearance-font-card__sample" :style="{ fontFamily: preset.stack }">
              Aa 节点
            </span>
            <span class="appearance-font-card__name">{{ $t(preset.labelKey) }}</span>
          </button>
        </div>

        <b-field
          v-if="fontPreset === 'custom'"
          :label="$t('appearance.customFontLabel')"
          :message="$t('appearance.customFontHelp')"
          style="margin-top: 0.85rem"
        >
          <b-input
            v-model="customFont"
            :placeholder="$t('appearance.customFontPlaceholder')"
            @input="persist"
          />
        </b-field>
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
        <div class="appearance-preview__label">{{ $t("appearance.previewTitle") }}</div>
        <div class="appearance-preview__content">
          <div class="appearance-preview__row">
            <strong>{{ $t("appearance.previewSampleTitle") }}</strong>
            <span class="appearance-preview__latency">42 ms</span>
          </div>
          <p class="appearance-preview__desc">{{ $t("appearance.previewSampleDesc") }}</p>
          <code class="appearance-preview__code code-font">vless.example.com:443 · TLS · XHTTP</code>
        </div>
      </div>
    </section>

    <footer class="modal-card-foot appearance-footer">
      <b-button size="is-small" icon-left="restore" @click="handleReset">
        {{ $t("appearance.reset") }}
      </b-button>
      <b-button type="is-primary" @click="$parent.close()">
        {{ $t("operations.close") }}
      </b-button>
    </footer>
  </div>
</template>

<script>
import {
  FONT_PRESETS,
  FONT_SIZE_MAX,
  FONT_SIZE_MIN,
  LAYOUT_WIDTH_PRESETS,
  getAppearanceSettings,
  resetAppearanceSettings,
  saveAppearanceSettings,
} from "@/assets/js/appearance";

export default {
  name: "ModalAppearance",
  data() {
    const initial = getAppearanceSettings();
    return {
      fontPreset: initial.fontPreset,
      customFont: initial.customFont,
      fontSize: initial.fontSize,
      layoutWidth: initial.layoutWidth,
      fontPresets: FONT_PRESETS,
      layoutWidthPresets: LAYOUT_WIDTH_PRESETS,
      minSize: FONT_SIZE_MIN,
      maxSize: FONT_SIZE_MAX,
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
  },
  methods: {
    persist() {
      saveAppearanceSettings({
        fontPreset: this.fontPreset,
        customFont: this.customFont,
        fontSize: this.fontSize,
        layoutWidth: this.layoutWidth,
      });
    },
    selectFontSize(val) {
      this.fontSize = val;
      this.persist();
    },
    selectFontPreset(id) {
      this.fontPreset = id;
      this.persist();
    },
    selectLayoutWidth(id) {
      this.layoutWidth = id;
      this.persist();
    },
    handleReset() {
      const defaults = resetAppearanceSettings();
      this.fontPreset = defaults.fontPreset;
      this.customFont = defaults.customFont;
      this.fontSize = defaults.fontSize;
      this.layoutWidth = defaults.layoutWidth;
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
.appearance-modal {
  max-width: 560px;
  width: calc(100vw - 2rem);
  margin: auto;
}

.appearance-section__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  margin-bottom: 0.55rem;

  .label {
    margin-bottom: 0;
  }
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
  margin: 1.1rem 0;
}

.appearance-font-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.55rem;
}

.appearance-font-card {
  appearance: none;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 0.25rem;
  padding: 0.65rem 0.75rem;
  border-radius: var(--va-radius-sm);
  border: 1px solid var(--va-border);
  background: var(--va-surface-subtle);
  color: var(--va-text);
  font: inherit;
  cursor: pointer;
  text-align: left;
  transition: border-color 0.15s ease, background-color 0.15s ease;

  &:hover {
    border-color: var(--va-primary);
    background: var(--va-primary-soft);
  }

  &.is-selected {
    border-color: var(--va-primary);
    background: var(--va-primary-soft);
    box-shadow: inset 0 0 0 1px var(--va-primary);
  }
}

.appearance-font-card__sample {
  font-size: 1.05rem;
  font-weight: 600;
  color: var(--va-text);
}

.appearance-font-card__name {
  font-size: 0.76rem;
  color: var(--va-text-muted);
}

.appearance-preview {
  margin-top: 1.1rem;
  padding: 0.85rem 1rem;
  border-radius: var(--va-radius-sm);
  border: 1px solid var(--va-border);
  background: var(--va-surface-subtle);
}

.appearance-preview__label {
  font-size: 0.74rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--va-text-subtle);
  margin-bottom: 0.45rem;
}

.appearance-preview__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  margin-bottom: 0.25rem;
}

.appearance-preview__latency {
  font-size: 0.85rem;
  font-weight: 700;
  color: var(--va-latency-good);
  font-variant-numeric: tabular-nums;
}

.appearance-preview__desc {
  font-size: 0.88rem;
  color: var(--va-text-muted);
  margin-bottom: 0.35rem;
}

.appearance-preview__code {
  font-size: 0.8rem;
}

.appearance-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

@media screen and (max-width: 520px) {
  .appearance-font-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
