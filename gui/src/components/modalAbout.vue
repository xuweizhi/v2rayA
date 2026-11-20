<template>
  <div class="modal-card about-modal">
    <header class="modal-card-head">
      <p class="modal-card-title">mzz2017 / v2rayA</p>
      <button
        type="button"
        class="delete"
        :aria-label="$t('operations.close')"
        @click="$parent.close()"
      ></button>
    </header>
    <section class="modal-card-body lazy">
      <!-- eslint-disable-next-line vue/no-v-html -->
      <div v-html="$t('about')"></div>
    </section>
    <footer class="modal-card-foot about-footer">
      <a
        class="about-badges"
        href="https://github.com/v2rayA/v2rayA"
        target="_blank"
        rel="noopener noreferrer"
      >
        <img
          class="leave-right"
          src="https://img.shields.io/github/stars/mzz2017/v2rayA.svg?style=social"
          alt="stars"
        />
        <img
          class="leave-right"
          src="https://img.shields.io/github/forks/mzz2017/v2rayA.svg?style=social"
          alt="forks"
        />
        <img
          class="leave-right"
          src="https://img.shields.io/github/watchers/mzz2017/v2rayA.svg?style=social"
          alt="watchers"
        />
      </a>
      <b-button type="is-light" size="is-small" @click="$parent.close()">
        {{ $t("operations.close") }}
      </b-button>
    </footer>
  </div>
</template>

<script>
export default {
  name: "ModalAbout",
};
</script>

<style lang="scss" scoped>
.about-modal {
  max-width: 600px;
  width: calc(100vw - 2rem);
  margin: auto;
}

.about-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}

.about-badges {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.25rem;
}
</style>
