<template>
  <div class="modal-card appearance-modal">
    <header class="modal-card-head">
      <p class="modal-card-title">
        <i class="mdi mdi-palette-outline" style="margin-right: 0.4rem" aria-hidden="true"></i>
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
      <AppearancePanel ref="panel" />
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
import AppearancePanel from "@/components/appearancePanel";

export default {
  name: "ModalAppearance",
  components: { AppearancePanel },
  methods: {
    handleReset() {
      if (this.$refs.panel) {
        this.$refs.panel.handleReset();
      }
    },
  },
};
</script>

<style lang="scss" scoped>
.appearance-modal {
  max-width: 640px;
  width: calc(100vw - 2rem);
  margin: auto;
}

.appearance-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
</style>
