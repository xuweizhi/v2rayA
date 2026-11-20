<template>
  <div class="modal-card" style="max-width: 450px; margin: auto">
    <header class="modal-card-head">
      <p class="modal-card-title">
        {{ first ? $t("register.title") : `${$t("login.title")} - v2rayA` }}
      </p>
    </header>
    <section class="modal-card-body">
      <p style="text-align: center">
        <img src="@/assets/img/logo2.png" alt="v2rayA" />
      </p>
      <b-field :label="$t('login.username')">
        <b-input
          ref="username"
          v-model="username"
          autocomplete="username"
          @input="validationError = ''"
          @keyup.enter.native="handleEnter"
        ></b-input>
      </b-field>
      <b-field :label="$t('login.password')">
        <b-input
          v-model="password"
          type="password"
          password-reveal
          :autocomplete="first ? 'new-password' : 'current-password'"
          :maxlength="first ? '32' : ''"
          @input="validationError = ''"
          @keyup.enter.native="handleEnter"
        ></b-input>
      </b-field>
      <p v-if="validationError" role="alert" class="help is-danger" style="margin-bottom: 0.75rem">
        {{ validationError }}
      </p>
      <b-message v-if="first" type="is-info" class="after-line-dot5">
        <p>{{ $t("register.messages.0") }}</p>
        <p>{{ $t("register.messages.1") }}</p>
        <p>{{ $t("register.messages.2") }}</p>
      </b-message>
    </section>
    <footer class="modal-card-foot flex-end">
      <b-button
        type="is-primary"
        :loading="isSubmitting"
        :disabled="isSubmitting"
        @click="handleClickSubmit"
      >
        {{ first ? $t("operations.create") : $t("operations.login") }}
      </b-button>
    </footer>
  </div>
</template>

<script>
import { isBackendRequest } from "@/assets/js/backend";
import { handleResponse } from "@/assets/js/utils";
import i18n from "@/plugins/i18n";

export default {
  i18n,
  name: "ModalLogin",
  props: {
    first: {
      type: Boolean,
      default: false,
    },
  },
  data: () => ({
    username: "",
    password: "",
    isSubmitting: false,
    validationError: "",
  }),
  mounted() {
    this.$refs.username.focus();
  },
  methods: {
    handleClickSubmit() {
      if (this.isSubmitting) {
        return;
      }
      if (!this.username.trim() || !this.password) {
        this.validationError = this.$t("loginValidation.emptyCredentials");
        return;
      }
      this.validationError = "";
      this.isSubmitting = true;
      if (this.first) {
        //register
        this.$axios({
          url: apiRoot + "/account",
          method: "post",
          data: {
            username: this.username,
            password: this.password,
          },
        }).then((res) => {
          handleResponse(res, this, () => {
            if (!isBackendRequest(res.config)) return;
            localStorage["token"] = res.data.data.token;
            this.$emit("close");
            this.$remount();
          });
        }).finally(() => {
          this.isSubmitting = false;
        });
      } else {
        //login
        this.$axios({
          url: apiRoot + "/login",
          method: "post",
          data: {
            username: this.username,
            password: this.password,
          },
        }).then((res) => {
          handleResponse(res, this, () => {
            if (!isBackendRequest(res.config)) return;
            localStorage["token"] = res.data.data.token;
            this.$emit("close");
            this.$remount();
          });
        }).finally(() => {
          this.isSubmitting = false;
        });
      }
    },
    handleEnter() {
      if (!this.isSubmitting) {
        this.handleClickSubmit();
      }
    },
  },
};
</script>

<style lang="scss">
.modal-login .modal-background {
  background-color: rgba(10, 10, 10, 0.7) !important;
}

.modal-login .modal-background,
.modal-login-app .modal-background,
.modal-login-programmatic .modal-background {
  pointer-events: none !important;
}

.modal-login-programmatic {
  z-index: 4500 !important;
}

.modal-login-programmatic .modal-content,
.modal-login-programmatic .animation-content,
.modal-login-programmatic .modal-card {
  position: relative;
  z-index: 1 !important;
  pointer-events: auto !important;
}
</style>
<style lang="scss" scoped>
.after-line-dot5 {
  font-size: 14px;
  p {
    font-size: 14px;
  }
}
</style>
