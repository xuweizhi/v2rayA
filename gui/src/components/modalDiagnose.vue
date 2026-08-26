<template>
  <div class="modal-card" style="max-width: 640px; margin: auto">
    <header class="modal-card-head">
      <p class="modal-card-title">{{ $t("diagnose.title") }}</p>
    </header>
    <section class="modal-card-body">
      <div class="columns">
        <div class="column is-8">
          <b-field :label="$t('diagnose.domain')">
            <b-input v-model="domain" placeholder="www.google.com" />
          </b-field>
        </div>
        <div class="column is-4">
          <b-field>
            <b-button
              type="is-primary"
              :loading="loading"
              style="margin-top: 1.7rem"
              @click="run"
            >
              {{ $t("diagnose.run") }}
            </b-button>
          </b-field>
        </div>
      </div>
      <div v-if="report" class="diagnose-report">
        <div class="diagnose-toolbar">
          <b-button size="is-small" type="is-info" @click="copyReport">
            {{ $t("diagnose.copy") }}
          </b-button>
        </div>
        <pre class="diagnose-text">{{ report.text }}</pre>
      </div>
    </section>
  </div>
</template>

<script>
import { handleResponse } from "@/assets/js/utils";
export default {
  name: "ModalDiagnose",
  data: () => ({
    domain: "",
    loading: false,
    report: null,
  }),
  methods: {
    run() {
      this.loading = true;
      this.report = null;
      this.$axios({
        url: apiRoot + "/diagnose?domain=" + encodeURIComponent(this.domain),
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
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(() => {
          this.$buefy.toast.open({
            message: this.$t("common.success"),
            type: "is-primary",
            position: "is-top",
            duration: 2000,
            queue: false,
          });
        });
      } else {
        const ta = document.createElement("textarea");
        ta.value = text;
        document.body.appendChild(ta);
        ta.select();
        document.execCommand("copy");
        document.body.removeChild(ta);
      }
    },
  },
};
</script>
