<template>
  <div class="modal-card" style="max-width: 620px; margin: auto">
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
        <b-table-column field="size" :label="$t('backup.size')" width="90" v-slot="p">
          {{ formatBytes(p.row.size) }}
        </b-table-column>
        <b-table-column field="modTime" :label="$t('backup.time')" width="160" v-slot="p">
          {{ formatDate(p.row.modTime) }}
        </b-table-column>
        <b-table-column :label="$t('operations.name')" width="190" v-slot="p">
          <a
            class="button is-small is-success"
            :href="apiRoot + '/backupDownload?filename=' + encodeURIComponent(p.row.name) + '&Authorization=' + encodeURIComponent(localStorage['token'] || '')"
            target="_blank"
            rel="noopener noreferrer"
          >
            {{ $t("operations.export") }}
          </a>
          <b-button
            size="is-small"
            type="is-danger"
            outlined
            @click="handleRestore('local', p.row.name)"
          >
            {{ $t("backup.restore") }}
          </b-button>
        </b-table-column>
      </b-table>

      <hr />
      <b-field :label="$t('backup.webdavUrl')" label-position="on-border">
        <b-input v-model="webdav.url" placeholder="https://dav.example.com/dav/v2raya" />
      </b-field>
      <div class="columns">
        <div class="column">
          <b-field :label="$t('backup.webdavUsername')" label-position="on-border">
            <b-input v-model="webdav.username" />
          </b-field>
        </div>
        <div class="column">
          <b-field :label="$t('backup.webdavPassword')" label-position="on-border">
            <b-input v-model="webdav.password" type="password" password-reveal />
          </b-field>
        </div>
      </div>
      <b-field :label="$t('backup.webdavConnectionMode')" label-position="on-border">
        <b-select v-model="webdav.connectionMode" expanded>
          <option value="followSubscription">{{ $t('backup.connectionModes.followSubscription') }}</option>
          <option value="onlyDirect">{{ $t('backup.connectionModes.onlyDirect') }}</option>
          <option value="onlyProxy">{{ $t('backup.connectionModes.onlyProxy') }}</option>
          <option value="preferDirect">{{ $t('backup.connectionModes.preferDirect') }}</option>
          <option value="preferProxy">{{ $t('backup.connectionModes.preferProxy') }}</option>
        </b-select>
      </b-field>
      <div class="backup-toolbar">
        <b-button size="is-small" @click="handleSaveWebdav">
          {{ $t("backup.saveWebdav") }}
        </b-button>
        <b-button size="is-small" type="is-primary" :loading="uploading" :disabled="!webdav.url" @click="handleUpload">
          {{ $t("backup.uploadLatest") }}
        </b-button>
        <b-button size="is-small" :loading="listing" :disabled="!webdav.url" @click="handleListRemote">
          {{ $t("backup.refreshRemote") }}
        </b-button>
      </div>
      <b-table v-if="remoteItems.length || listed" :data="remoteItems" :per-page="100" hoverable striped>
        <b-table-column field="name" :label="$t('backup.remoteFiles')" v-slot="p">
          {{ p.row.name }}
        </b-table-column>
        <b-table-column field="size" :label="$t('backup.size')" width="90" v-slot="p">
          {{ formatBytes(p.row.size) }}
        </b-table-column>
        <b-table-column :label="$t('operations.name')" width="190" v-slot="p">
          <a
            class="button is-small is-success"
            :href="apiRoot + '/webdavBackupDownload?filename=' + encodeURIComponent(p.row.name) + '&Authorization=' + encodeURIComponent(localStorage['token'] || '')"
            target="_blank"
            rel="noopener noreferrer"
          >
            {{ $t("operations.export") }}
          </a>
          <b-button
            size="is-small"
            type="is-danger"
            outlined
            @click="handleRestore('webdav', p.row.name)"
          >
            {{ $t("backup.restore") }}
          </b-button>
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
    remoteItems: [],
    listed: false,
    creating: false,
    uploading: false,
    listing: false,
    webdav: { url: "", username: "", password: "", connectionMode: "followSubscription" },
  }),
  created() {
    this.load();
    this.$axios({ url: apiRoot + "/setting", method: "get" }).then((res) => {
      if (res.data && res.data.code === "SUCCESS" && res.data.data) {
        const s = res.data.data.setting;
        this.webdav.url = s.webdavUrl || "";
        this.webdav.username = s.webdavUsername || "";
        this.webdav.password = s.webdavPassword || "";
        this.webdav.connectionMode = s.webdavConnectionMode || "followSubscription";
      }
    });
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
    saveWebdavConfig() {
      // PUT the whole setting object with the webdav fields patched in.
      return this.$axios({ url: apiRoot + "/setting", method: "get" }).then((res) => {
        if (
          !res.data ||
          res.data.code !== "SUCCESS" ||
          !res.data.data ||
          !res.data.data.setting
        ) {
          return Promise.reject(new Error("failed to load setting"));
        }
        const s = Object.assign({}, res.data.data.setting);
        s.webdavUrl = this.webdav.url;
        s.webdavUsername = this.webdav.username;
        s.webdavPassword = this.webdav.password;
        s.webdavConnectionMode = this.webdav.connectionMode;
        return this.$axios({ url: apiRoot + "/setting", method: "put", data: s });
      }).then((res) => {
        if (!res.data || res.data.code !== "SUCCESS") {
          throw new Error(res.data?.message || this.$t("common.fail"));
        }
        return res;
      });
    },
    showWebdavError(err) {
      this.$buefy.toast.open({
        message: err?.response?.data?.message || err?.message || this.$t("common.fail"),
        type: "is-warning",
        position: "is-top",
        queue: false,
        duration: 5000,
      });
    },
    handleSaveWebdav() {
      this.saveWebdavConfig()
        .then(() => {
          this.$buefy.toast.open({
            message: this.$t("common.success"),
            type: "is-primary",
            position: "is-top",
            duration: 3000,
            queue: false,
          });
        })
        .catch((err) => this.showWebdavError(err));
    },
    handleUpload() {
      this.uploading = true;
      this.saveWebdavConfig()
        .then(() => this.$axios({ url: apiRoot + "/webdavBackup", method: "post" }))
        .then((res) => {
          handleResponse(res, this, () => {
            this.$buefy.toast.open({
              message: this.$t("common.success"),
              type: "is-primary",
              position: "is-top",
              duration: 3000,
              queue: false,
            });
            this.handleListRemote();
          });
        })
        .catch((err) => this.showWebdavError(err))
        .finally(() => {
          this.uploading = false;
        });
    },
    handleListRemote() {
      this.listing = true;
      this.$axios({ url: apiRoot + "/webdavBackups", method: "get" })
        .then((res) => {
          handleResponse(res, this, () => {
            this.remoteItems = res.data.data.webdavBackups || [];
            this.listed = true;
          });
        })
        .finally(() => {
          this.listing = false;
        });
    },
    handleRestore(source, name) {
      this.$buefy.dialog.confirm({
        title: this.$t("backup.restore"),
        message: this.$t("backup.restoreConfirm"),
        confirmText: this.$t("operations.confirm"),
        cancelText: this.$t("operations.cancel"),
        type: "is-danger",
        hasIcon: true,
        onConfirm: () => {
          this.$axios({
            url: apiRoot + "/backupRestore",
            method: "post",
            data: { source, name },
          }).then((res) => {
            handleResponse(res, this, () => {
              this.$buefy.toast.open({
                message: this.$t("backup.restoring"),
                type: "is-primary",
                position: "is-top",
                duration: 3000,
                queue: false,
              });
            });
          });
        },
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
