<template>
  <div class="modal-card import-card">
    <header class="modal-card-head">
      <p class="modal-card-title">{{ $t("operations.import") }}</p>
    </header>
    <section class="modal-card-body">
      <template v-if="!result">
        <b-field :label="reviewed ? $t('import.url') : $t('import.source')">
          <b-input ref="source" v-model="source" :type="reviewed ? 'text' : 'textarea'"
            rows="3" :disabled="busy" spellcheck="false" autocapitalize="off"
            @input="sourceEdited" @keydown.ctrl.enter.native.prevent="submit" />
        </b-field>
        <div v-if="!reviewed" class="field">
          <label class="button is-light file-pick" :class="{ 'is-loading': reading }">
            <input type="file" :disabled="busy" accept=".txt,.yaml,.yml,.json,image/*" @change="selectFile" />
            <span>{{ $t('import.file') }}</span>
          </label>
          <p class="help">{{ fileName || $t('import.formats') }}</p>
        </div>
        <template v-if="reviewed">
          <b-field :label="$t('subscription.remarks')">
            <b-input v-model="remarks" :disabled="busy" />
          </b-field>
          <b-field :label="$t('subscription.nodeDNS')" :message="$t('subscription.nodeDNSHelp')">
            <b-input v-model="dns" :disabled="busy" type="textarea" rows="2" spellcheck="false" />
          </b-field>
        </template>
        <details v-if="!fileName" class="field">
          <summary>{{ $t('import.credentials') }}</summary>
          <b-field :label="$t('subscription.decryptPassword')">
            <b-input v-model="password" :disabled="busy" type="password" autocomplete="off" password-reveal />
          </b-field>
          <b-field :label="$t('subscription.ageCredential')">
            <AgeCredential v-model="agePassword" :disabled="busy" />
          </b-field>
        </details>
        <p v-if="error" role="alert" class="help is-danger">{{ error }}</p>
      </template>
      <div v-else role="status">
        <p>{{ $t('import.summary', result) }}</p>
        <ul class="skip-reasons"><li v-for="(reason, i) in result.reasons" :key="i">{{ reason }}</li></ul>
      </div>
    </section>
    <footer class="modal-card-foot flex-end">
      <b-button :disabled="busy" @click="$emit('close')">
        {{ result ? $t('import.close') : $t('operations.cancel') }}
      </b-button>
      <b-button v-if="!result" type="is-primary" :disabled="busy || !source.trim()" :loading="sending" @click="submit">
        {{ needsReview ? $t('import.review') : $t('operations.confirm') }}
      </b-button>
    </footer>
  </div>
</template>

<script>
import { Decoder } from "@nuintun/qrcode";
import AgeCredential from "./AgeCredential.vue";

export default {
  name: "ModalImport",
  components: { AgeCredential },
  data: () => ({
    source: "", fileName: "", remarks: "", dns: "", password: "", agePassword: "",
    reviewed: false, hadLinkDNS: false, reading: false, sending: false, result: null, error: "",
  }),
  computed: {
    busy() { return this.reading || this.sending; },
    needsReview() {
      const text = this.source.trim();
      if (/^(karing|clash|sing-box):\/\//i.test(text)) return true;
      if (this.reviewed || this.fileName || !/^https:\/\//i.test(text) || /\s/.test(text)) return false;
      try { return new URL(text).searchParams.has("outbound-dns"); }
      catch { return false; }
    },
  },
  watch: {
    busy(value) { this.$emit("busy", value); },
  },
  mounted() { this.$refs.source.focus(); },
  methods: {
    sourceEdited() {
      this.fileName = "";
      this.error = "";
    },
    async selectFile(event) {
      const file = event.target.files[0];
      event.target.value = "";
      if (!file || this.busy) return;
      this.reading = true;
      this.error = "";
      const isImage = file.type.startsWith("image/");
      try {
        if (isImage) {
          const url = URL.createObjectURL(file);
          try { this.source = (await new Decoder().scan(url)).data; }
          finally { URL.revokeObjectURL(url); }
          this.fileName = "";
        } else {
          this.source = await file.text();
          this.fileName = file.name;
          if (!this.source.trim()) this.error = this.$t("import.emptyFile");
        }
      } catch {
        this.source = "";
        this.fileName = "";
        this.error = this.$t(isImage ? "import.qrcodeError" : "import.fileError");
      } finally { this.reading = false; }
    },
    async submit() {
      if (this.busy || this.result || !this.source.trim()) return;
      this.sending = true;
      this.error = "";
      try {
        if (this.needsReview) {
          const { data } = await this.$axios.post(apiRoot + "/importLink", { url: this.source.trim() });
          if (data.code !== "SUCCESS") { this.error = data.message; return; }
          this.source = data.data.url;
          this.remarks = data.data.remarks || "";
          this.dns = (data.data.nodeDNS || []).join("\n");
          this.hadLinkDNS = Boolean(this.dns);
          this.fileName = "";
          this.reviewed = true;
          return;
        }
        if (this.reviewed && !/^https?:\/\//i.test(this.source.trim())) {
          this.error = this.$t("import.invalidSubscription");
          return;
        }
        const dns = this.dns.split(/[\n,]+/).map(s => s.trim()).filter(Boolean);
        const { data } = await this.$axios.post(apiRoot + "/import", {
          url: this.source.trim(), content: Boolean(this.fileName),
          remarks: this.reviewed ? this.remarks : undefined,
          nodeDNS: this.reviewed && (this.hadLinkDNS || dns.length) ? dns : undefined,
          password: this.password || undefined, agePassword: this.agePassword || undefined,
        });
        if (data.code !== "SUCCESS") { this.error = data.message; return; }
        this.$emit("imported");
        const result = data.data.importResult;
        if (result && result.skipped) {
          this.result = result;
        } else {
          this.$buefy.toast.open({ message: result ? this.$t("import.summary", result) : this.$t("common.success"), type: "is-primary" });
          this.$emit("close");
        }
      } catch (err) {
        this.error = err.message || this.$t("common.fail");
      } finally {
        this.sending = false;
        this.$emit("busy", false);
      }
    },
  },
};
</script>

<style scoped>
.import-card { width: min(32rem, 92vw); margin: auto; }
.file-pick { position: relative; overflow: hidden; }
.file-pick:focus-within { outline: 2px solid currentColor; }
.file-pick input { position: absolute; inset: 0; width: 100%; opacity: 0; cursor: pointer; }
summary { cursor: pointer; margin-bottom: .5rem; }
.skip-reasons { list-style: disc; margin: .5rem 0 0 1.25rem; overflow-wrap: anywhere; }
</style>
