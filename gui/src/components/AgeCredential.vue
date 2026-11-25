<template>
  <div class="age-credential control">
    <textarea
      class="textarea"
      :class="{ concealed: !revealed }"
      :value="value"
      :disabled="disabled"
      :placeholder="placeholder"
      :aria-label="$t('subscription.ageCredential')"
      :rows="(value || '').includes('\n') ? 3 : 1"
      autocomplete="off"
      autocorrect="off"
      autocapitalize="off"
      spellcheck="false"
      @input="$emit('input', $event.target.value)"
      @copy="protectClipboard"
      @cut="protectClipboard"
    ></textarea>
    <button type="button" class="reveal" :disabled="disabled"
      :aria-label="$t(revealed ? 'subscription.hideAgeCredential' : 'subscription.showAgeCredential')"
      @click="revealed = !revealed">
      <b-icon :icon="revealed ? 'eye-off' : 'eye'" />
    </button>
  </div>
</template>

<script>
export default {
  name: "AgeCredential",
  props: {
    value: { type: String, default: "" },
    disabled: Boolean,
    placeholder: { type: String, default: "" },
  },
  data: () => ({ revealed: false }),
  watch: {
    disabled(value) {
      if (value) this.revealed = false;
    },
  },
  methods: {
    protectClipboard(event) {
      if (!this.revealed) event.preventDefault();
    },
  },
};
</script>

<style scoped>
.age-credential { position: relative; }
.textarea { padding-right: 2.75em; resize: vertical; }
/* Hide the glyphs even on engines without text-security or in forced colors. */
.age-credential .concealed {
  color: transparent !important;
  text-shadow: none;
  caret-color: #777;
  forced-color-adjust: none;
}
.concealed::selection { color: transparent !important; }
.concealed::placeholder { color: #767676; }
@supports (-webkit-text-security: disc) {
  .age-credential .concealed {
    color: inherit !important;
    -webkit-text-security: disc;
    forced-color-adjust: auto;
  }
  .concealed::selection { color: HighlightText !important; }
  .concealed::placeholder { -webkit-text-security: none; }
}
.reveal {
  position: absolute;
  right: 1px;
  top: 1px;
  height: calc(2.5em - 2px);
  width: 2.5em;
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
}
.reveal:disabled { cursor: default; }
</style>
