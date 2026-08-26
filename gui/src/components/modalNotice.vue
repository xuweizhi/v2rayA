<template>
  <div class="modal-card" style="max-width: 560px; margin: auto">
    <header class="modal-card-head">
      <p class="modal-card-title">{{ $t("notice.title") }}</p>
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
          const ids = {};
          // Mark everything as read on open; fetch read state first.
          this.$axios({ url: apiRoot + "/noticesRead", method: "post", data: { ids: this.notices.map((n) => n.id) } });
          this.notices.forEach((n) => {
            ids[n.id] = true;
          });
          this.readIds = ids;
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
