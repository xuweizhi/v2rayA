<template>
  <div class="modal-card diagnose-modal">
    <header class="modal-card-head">
      <p class="modal-card-title">{{ $t("diagnose.title") }}</p>
      <button
        type="button"
        class="delete"
        :aria-label="$t('operations.close')"
        @click="$parent.close()"
      ></button>
    </header>
    <section class="modal-card-body">
      <b-field :label="$t('diagnose.outbound')">
        <b-select v-model="outbound" expanded>
          <option value="direct">{{ $t("dns.direct") }}</option>
          <option v-for="group in outbounds" :key="group" :value="group">{{ group }}</option>
        </b-select>
      </b-field>
      <div class="diagnose-query-row">
        <b-field :label="$t('diagnose.domain')" class="diagnose-domain-field">
          <b-input
            v-model="domain"
            placeholder="www.google.com"
            @keyup.enter.native="run"
          />
        </b-field>
        <b-button
          type="is-primary"
          :loading="loading"
          icon-left="play"
          class="diagnose-run-btn"
          @click="run"
        >
          {{ $t("diagnose.run") }}
        </b-button>
      </div>
      <div v-if="report" class="diagnose-report">
        <div class="diagnose-toolbar">
          <b-button size="is-small" type="is-info" outlined icon-left="content-copy" @click="copyReport">
            {{ $t("diagnose.copy") }}
          </b-button>
        </div>
        <pre class="diagnose-text code-font">{{ report.text }}</pre>
      </div>
    </section>
    <footer class="modal-card-foot flex-end">
      <b-button @click="$parent.close()">{{ $t("operations.close") }}</b-button>
    </footer>
  </div>
</template>

<script>
import { handleResponse } from "@/assets/js/utils";
export default {
  name: "ModalDiagnose",
  props: { initialOutbound: { type: String, default: 'proxy' } },
  data() { return {
    outbound: this.initialOutbound,
    outbounds: ['proxy'],
    domain: "",
    loading: false,
    report: null,
  }; },
  created() {
    this.$axios.get(apiRoot + '/outbounds').then(res => handleResponse(res, this, () => { this.outbounds = res.data.data.outbounds; }));
  },
  methods: {
    run() {
      if (this.loading) return;
      this.loading = true;
      this.report = null;
      this.$axios({
        url: apiRoot + "/diagnose",
        params: { domain: this.domain, outbound: this.outbound },
        method: "get",
        timeout: 60000,
      })
        .then((res) => {
          handleResponse(res, this, () => {
            this.report = res.data.data.diagnose;
          });
        })
        .finally(() => {
          this.loading = false;
        });
    },
    copyReport() {
      if (!this.report) return;
      const text = this.report.text;
      const notifySuccess = () => {
        this.$buefy.toast.open({
          message: this.$t("common.success"),
          type: "is-success",
          position: "is-top",
          duration: 2000,
          queue: false,
        });
      };
      const fallback = () => {
        const ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        try {
          if (!document.execCommand("copy")) throw new Error("copy failed");
          notifySuccess();
        } catch {
          this.$buefy.toast.open({ message: this.$t("common.fail"), type: "is-warning" });
        } finally {
          ta.remove();
        }
      };
      if (navigator.clipboard?.writeText) {
        navigator.clipboard.writeText(text).then(notifySuccess, fallback);
      } else {
        fallback();
      }
    },
  },
};
</script>

<style lang="scss" scoped>
.diagnose-modal {
  max-width: 680px;
  width: calc(100vw - 2rem);
  margin: auto;
}

.diagnose-query-row {
  display: flex;
  align-items: flex-end;
  gap: 0.75rem;
  flex-wrap: wrap;
}

.diagnose-domain-field {
  flex: 1 1 260px;
  margin-bottom: 0 !important;
}

.diagnose-run-btn {
  flex-shrink: 0;
}

.diagnose-report {
  margin-top: 1.1rem;
}

.diagnose-toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 0.5rem;
}

.diagnose-text {
  max-height: 45vh;
  overflow: auto;
  padding: 0.85rem 1rem;
  border-radius: var(--va-radius-sm);
  border: 1px solid var(--va-border);
  background: var(--va-surface-subtle);
  color: var(--va-text);
  font-size: 0.82rem;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
