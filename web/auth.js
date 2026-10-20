const form = document.querySelector("#auth-form");
const errorBox = document.querySelector("#auth-error");
const submit = document.querySelector("#auth-submit");

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  errorBox.hidden = true;
  submit.disabled = true;
  submit.textContent = "Opening workspace…";

  const formValues = Object.fromEntries(new FormData(form));
  try {
    const response = await fetch("/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ identity: formValues.identity, password: formValues.password }),
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || "Could not open the workspace.");
    window.location.assign("/dashboard");
  } catch (error) {
    errorBox.textContent = error.message;
    errorBox.hidden = false;
  } finally {
    submit.disabled = false;
    submit.textContent = "Open workspace";
  }
});
