<template>
  <div class="modal-card" style="max-width: 560px; margin: auto">
    <header class="modal-card-head">
      <p class="modal-card-title">{{ $t("backup.title") }}</p>
    </header>
    <section class="modal-card-body">
      <div class="backup-toolbar">
        <b-button type="is-primary" size="is-small" :loading="creating" @click="handleCreate">
          {{ $t("backup.createNow") }}
        </b-button>
        <span class="backup-note">{{ $t("backup.note") }}</span>
      </div>
      <b-table :data="backups" :per-page="100" hoverable striped>
        <b-table-column field="name" :label="$t('backup.name')" v-slot="p">
          {{ p.row.name }}
        </b-table-column>
        <b-table-column field="size" :label="$t('backup.size')" width="100" v-slot="p">
          {{ formatBytes(p.row.size) }}
        </b-table-column>
        <b-table-column field="modTime" :label="$t('backup.time')" width="170" v-slot="p">
          {{ formatDate(p.row.modTime) }}
        </b-table-column>
        <b-table-column :label="$t('operations.name')" width="90" v-slot="p">
          <a
            class="button is-small is-success"
            :href="apiRoot + '/backupDownload?filename=' + encodeURIComponent(p.row.name) + '&Authorization=' + encodeURIComponent(localStorage['token'] || '')"
            target="_blank"
            rel="noopener noreferrer"
          >
            {{ $t("operations.export") }}
          </a>
        </b-table-column>
      </b-table>
    </section>
  </div>
</template>

<script>
import { handleResponse } from "@/assets/js/utils";
export default {
  name: "ModalBackup",
  data: () => ({
    backups: [],
    creating: false,
  }),
  created() {
    this.load();
  },
  methods: {
    load() {
      this.$axios({ url: apiRoot + "/backups", method: "get" }).then((res) => {
        handleResponse(res, this, () => {
          this.backups = res.data.data.backups || [];
        });
      });
    },
    handleCreate() {
      this.creating = true;
      this.$axios({ url: apiRoot + "/backup", method: "post" })
        .then((res) => {
          handleResponse(res, this, () => {
            this.$buefy.toast.open({
              message: this.$t("common.success"),
              type: "is-primary",
              position: "is-top",
              duration: 3000,
              queue: false,
            });
            this.load();
          });
        })
        .finally(() => {
          this.creating = false;
        });
    },
    formatBytes(bytes) {
      if (!bytes) return "0B";
      const units = ["B", "KB", "MB", "GB"];
      let i = 0;
      let v = bytes;
      while (v >= 1024 && i < units.length - 1) {
        v /= 1024;
        i++;
      }
      return v.toFixed(v >= 100 || i === 0 ? 0 : 1) + units[i];
    },
    formatDate(unixSec) {
      const d = new Date(unixSec * 1000);
      const pad = (n) => String(n).padStart(2, "0");
      return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(
        d.getHours()
      )}:${pad(d.getMinutes())}`;
    },
  },
};
</script>
