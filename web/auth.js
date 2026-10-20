const signupMode = window.location.pathname === "/signup";
const form = document.querySelector("#auth-form");
const errorBox = document.querySelector("#auth-error");
const nameField = document.querySelector("#name-field");
const nameLabel = document.querySelector("#name-label");
const passwordField = document.querySelector("#password-field");

if (signupMode) {
  document.title = "Create your Crelan account";
  document.querySelector("#auth-eyebrow").textContent = "GET STARTED";
  document.querySelector("#auth-title").textContent = "Create your account";
  document.querySelector("#auth-subtitle").textContent = "Your personal workspace will be ready as soon as you sign up.";
  document.querySelector("#auth-submit").textContent = "Create account";
  document.querySelector("#switch-prompt").textContent = "Already have an account?";
  document.querySelector("#switch-link").textContent = "Sign in";
  document.querySelector("#switch-link").href = "/login";
  document.querySelector("#identity-label").textContent = "Email address";
  document.querySelector("#email-field").type = "email";
  document.querySelector("#email-field").autocomplete = "email";
  document.querySelector("#email-field").placeholder = "you@example.com";
  document.querySelector("#email-field").maxLength = 254;
  passwordField.minLength = 12;
  passwordField.placeholder = "At least 12 characters";
  nameField.hidden = false;
  nameField.required = true;
  nameLabel.hidden = false;
  passwordField.autocomplete = "new-password";
}

const authErrors = {
  github_not_configured: "GitHub sign-in is not configured yet. You can sign in with email or ask your administrator to configure the GitHub OAuth app.",
  github_cancelled: "GitHub sign-in was cancelled.",
  github_failed: "GitHub sign-in could not be completed. Please try again.",
};
const errorCode = new URLSearchParams(window.location.search).get("error");
if (errorCode) {
  errorBox.textContent = authErrors[errorCode] || "Sign-in could not be completed. Please try again.";
  errorBox.hidden = false;
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  errorBox.hidden = true;
  const submit = document.querySelector("#auth-submit");
  submit.disabled = true;
  submit.textContent = signupMode ? "Creating account…" : "Signing in…";

  const formValues = Object.fromEntries(new FormData(form));
  const payload = signupMode
    ? { name: formValues.name, email: formValues.identity, password: formValues.password }
    : { identity: formValues.identity, password: formValues.password };
  try {
    const response = await fetch(signupMode ? "/auth/signup" : "/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    const result = await response.json();
    if (!response.ok) {
      throw new Error(result.error || "Authentication failed. Please try again.");
    }
    window.location.assign("/dashboard");
  } catch (error) {
    errorBox.textContent = error.message;
    errorBox.hidden = false;
  } finally {
    submit.disabled = false;
    submit.textContent = signupMode ? "Create account" : "Sign in";
  }
});
