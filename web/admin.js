const $ = (selector) => document.querySelector(selector);

async function request(path) {
  const response = await fetch(path, { credentials: "same-origin" });
  if (response.status === 401) {
    window.location.assign("/login");
    throw new Error("Sign in required");
  }
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(body.error || `Request failed (${response.status})`);
    error.status = response.status;
    throw error;
  }
  return body;
}

async function loadAdminOverview() {
  const state = $("#admin-state");
  const output = $("#admin-overview-data");
  state.textContent = "Loading live platform overview…";
  output.hidden = true;
  try {
    const overview = await request("/api/v1/admin/overview");
    output.textContent = JSON.stringify(overview, null, 2);
    output.hidden = false;
    state.textContent = "Platform overview loaded.";
    const activeContext = overview.cluster?.context;
    $("#cluster-connect-state").textContent = activeContext
      ? `Active customer deployment cluster: ${activeContext}`
      : "No customer deployment cluster is connected.";
    $("#admin-api-status").textContent = "Admin API connected";
    const readiness = await request("/api/v1/admin/readiness");
    const checks = $("#readiness-checks");
    checks.replaceChildren();
    const table = document.createElement("table");
    table.className = "resource-table";
    const body = document.createElement("tbody");
    readiness.checks.forEach((check) => {
      const row = document.createElement("tr");
      const name = document.createElement("th");
      name.scope = "row";
      name.textContent = check.name;
      const status = document.createElement("td");
      status.textContent = check.ready ? "Ready" : "Needs attention";
      const detail = document.createElement("td");
      detail.textContent = check.detail;
      row.append(name, status, detail);
      body.append(row);
    });
    table.append(body);
    checks.append(table);
    $("#readiness-state").textContent = readiness.ready
      ? "All reported readiness checks passed. Validate operator health and restore procedures before production use."
      : "Some cluster prerequisites need attention before offering every database engine.";
  } catch (error) {
    state.textContent = error.status === 403
      ? "Your account does not have platform administrator access."
      : error.status === 404
        ? "The platform overview API is not available on this server yet."
        : error.message;
    $("#admin-api-status").textContent = error.status === 403 ? "Access denied" : "Admin API unavailable";
  }
}

async function initializeAdmin() {
  try {
    const user = await request("/api/v1/me");
    const name = user.name || user.email || "Administrator";
    $("#admin-name").textContent = name;
    $("#admin-email").textContent = user.email || "";
    $("#admin-avatar").textContent = name.charAt(0).toUpperCase();
    $("#admin-top-avatar").textContent = name.charAt(0).toUpperCase();
    if (user.platform_admin !== true) {
      $("#admin-state").textContent = "Your account does not have platform administrator access.";
      $("#admin-api-status").textContent = "Access denied";
      return;
    }
    await loadAdminOverview();
  } catch (error) {
    if (error.message !== "Sign in required") {
      $("#admin-state").textContent = error.message;
      $("#admin-api-status").textContent = "Could not check access";
    }
  }
}

$("#admin-refresh").addEventListener("click", loadAdminOverview);
$("#cluster-connect-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = $("#cluster-connect-submit");
  const state = $("#cluster-connect-state");
  const kubeconfig = $("#cluster-kubeconfig");
  button.disabled = true;
  state.textContent = "Checking the kubeconfig and connecting to the cluster…";
  try {
    const response = await fetch("/api/v1/admin/clusters/connect", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ kubeconfig: kubeconfig.value, context: $("#cluster-context").value.trim() }),
    });
    const body = await response.json().catch(() => ({}));
    if (response.status === 401) {
      window.location.assign("/login");
      return;
    }
    if (!response.ok) throw new Error(body.error || `Connection failed (${response.status})`);
    state.textContent = `Connected to “${body.context}”. The encrypted connection is saved; new customer workloads will use this cluster.`;
    kubeconfig.value = "";
    $("#cluster-context").value = "";
    await loadAdminOverview();
  } catch (error) {
    state.textContent = error.message;
  } finally {
    button.disabled = false;
  }
});
$("#admin-backup").addEventListener("click", async () => {
  const button = $("#admin-backup");
  button.disabled = true;
  try {
    const response = await fetch("/api/v1/admin/control-plane-backup", { method: "POST", credentials: "same-origin" });
    if (response.status === 401) {
      window.location.assign("/login");
      return;
    }
    if (!response.ok) {
      const body = await response.json().catch(() => ({}));
      throw new Error(body.error || `Backup failed (${response.status})`);
    }
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = "crelan-control-plane.db";
    link.click();
    URL.revokeObjectURL(url);
  } catch (error) {
    $("#admin-state").textContent = error.message;
  } finally {
    button.disabled = false;
  }
});
$("#admin-signout").addEventListener("click", async () => {
  await fetch("/auth/logout", { method: "POST", credentials: "same-origin" });
  window.location.assign("/login");
});

initializeAdmin();
