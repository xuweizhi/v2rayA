<template>
  <div class="modal-card" style="max-width: 580px; width: calc(100vw - 2rem); margin: auto">
    <header class="modal-card-head">
      <p class="modal-card-title">{{ $t("notice.title") }}</p>
      <button
        type="button"
        class="delete"
        :aria-label="$t('operations.close')"
        @click="$parent.close()"
      ></button>
    </header>
    <section class="modal-card-body">
      <b-message v-if="!notices.length" type="is-info">
        {{ $t("notice.empty") }}
      </b-message>
      <div v-for="n of notices" :key="n.id" class="card notice-card">
        <div class="card-content">
          <div class="notice-head">
            <b-tag v-if="!readIds[n.id]" type="is-danger" size="is-small">
              {{ $t("notice.unread") }}
            </b-tag>
            <strong>{{ n.title }}</strong>
            <span class="notice-time">{{ formatDate(n.updateTime) }}</span>
          </div>
          <div class="notice-content">{{ n.content }}</div>
          <a v-if="n.url" :href="n.url" target="_blank" rel="noopener noreferrer">
            {{ $t("notice.openLink") }}
          </a>
        </div>
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
  name: "ModalNotice",
  data: () => ({
    notices: [],
    readIds: {},
  }),
  created() {
    this.load();
  },
  methods: {
    load() {
      this.$axios({ url: apiRoot + "/notices", method: "get" }).then((res) => {
        handleResponse(res, this, () => {
          const data = res.data.data;
          this.notices = data.notices || [];
          this.readIds = data.readIds || {};
          const ids = this.notices.map(n => n.id).filter(id => !this.readIds[id]);
          if (ids.length) {
            this.$axios.post(apiRoot + "/noticesRead", { ids }).then((res) => {
              handleResponse(res, this, () => this.$emit("read"));
            });
          }
        });
      });
    },
    formatDate(unixSec) {
      const d = new Date(unixSec * 1000);
      const pad = (n) => String(n).padStart(2, "0");
      return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
    },
  },
};
</script>

<style scoped>
.notice-card {
  margin-bottom: 0.75rem;
  border: 1px solid var(--va-border);
  box-shadow: none;
}

.notice-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.45rem;
  margin-bottom: 0.45rem;
}

.notice-time {
  margin-left: auto;
  font-size: 0.78rem;
  color: var(--va-text-muted);
}

.notice-content {
  font-size: 0.9rem;
  line-height: 1.55;
  white-space: pre-wrap;
  margin-bottom: 0.4rem;
}
</style>
