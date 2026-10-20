const $ = (selector) => document.querySelector(selector);

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: { ...(options.body ? { "Content-Type": "application/json" } : {}), ...options.headers },
  });
  if (response.status === 401) {
    window.location.assign("/login");
    throw new Error("Sign in required");
  }
  if (response.status === 204) return null;
  const result = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(result.error || `Request failed (${response.status})`);
  return result;
}

async function loadAccount() {
  const user = await api("/api/v1/me");
  $("#account-name").textContent = user.name;
  $("#account-email").textContent = user.email;
  $("#top-avatar").textContent = user.name.charAt(0).toUpperCase();
  $(".user-avatar").textContent = user.name.charAt(0).toUpperCase();
}

function renderTable(container, columns, rows, emptyMessage) {
  container.replaceChildren();
  if (!rows.length) {
    const empty = document.createElement("p");
    empty.className = "resource-empty";
    empty.textContent = emptyMessage;
    container.append(empty);
    return;
  }
  const table = document.createElement("table");
  table.className = "resource-table";
  const head = document.createElement("thead");
  const headRow = document.createElement("tr");
  columns.forEach((column) => {
    const cell = document.createElement("th");
    cell.textContent = column.label;
    headRow.append(cell);
  });
  head.append(headRow);
  const body = document.createElement("tbody");
  rows.forEach((row) => {
    const tableRow = document.createElement("tr");
    columns.forEach((column) => {
      const cell = document.createElement("td");
      const value = column.value(row);
      cell.textContent = Array.isArray(value) ? (value.join(", ") || "—") : String(value ?? "—");
      tableRow.append(cell);
    });
    body.append(tableRow);
  });
  table.append(head, body);
  container.append(table);
}

async function loadClusterResources() {
  const error = $("#cluster-error");
  error.hidden = true;
  $("#cluster-status").textContent = "Loading cluster resources…";
  try {
    const result = await api("/api/v1/cluster/resources");
    $("#namespace-name").textContent = result.namespace;
    $("#pod-count").textContent = result.pods.length;
    $("#configmap-count").textContent = result.configmaps.length;
    $("#secret-count").textContent = result.secrets.length;
    renderTable($("#pods-list"), [
      { label: "Name", value: (pod) => pod.name },
      { label: "Status", value: (pod) => pod.phase || "Pending" },
      { label: "Ready", value: (pod) => pod.ready ? "Yes" : "No" },
      { label: "Restarts", value: (pod) => pod.restarts },
    ], result.pods, "No pods found in this namespace.");
    renderTable($("#configmaps-list"), [
      { label: "Name", value: (item) => item.name },
      { label: "Keys", value: (item) => item.keys },
    ], result.configmaps, "No ConfigMaps found in this namespace.");
    renderTable($("#secrets-list"), [
      { label: "Name", value: (item) => item.name },
      { label: "Type", value: (item) => item.type },
      { label: "Keys", value: (item) => item.keys },
    ], result.secrets, "No Secrets found in this namespace.");
    $("#cluster-status").textContent = "Cluster connected";
  } catch (cause) {
    error.textContent = cause.message;
    error.hidden = false;
    $("#cluster-status").textContent = "Cluster unavailable";
  }
}

async function loadClusterContexts() {
  const select = $("#cluster-context");
  const help = $("#cluster-connect-help");
  const error = $("#cluster-connect-error");
  error.hidden = true;
  try {
    const result = await api("/api/v1/cluster/contexts");
    const deploymentSelect = $("#deployment-cluster");
    deploymentSelect.replaceChildren();
    if (result.contexts.length) {
      result.contexts.forEach((contextName) => {
        const targetOption = document.createElement("option");
        targetOption.value = contextName;
        targetOption.textContent = contextName;
        deploymentSelect.append(targetOption);
      });
      if (result.active) deploymentSelect.value = result.active;
    } else {
      deploymentSelect.append(new Option("No clusters connected", ""));
    }
    select.replaceChildren();
    if (!result.contexts.length) {
      const option = document.createElement("option");
      option.value = "";
      option.textContent = result.in_cluster ? "No cluster choices available" : "No clusters configured";
      option.disabled = true;
      option.selected = true;
      select.append(option);
      help.textContent = result.in_cluster
        ? "Crelan is using its Kubernetes ServiceAccount. Ask the operator to configure additional clusters."
        : "No clusters are configured. Ask the operator to configure a cluster for Crelan.";
      return;
    }
    result.contexts.forEach((contextName) => {
      const option = document.createElement("option");
      option.value = contextName;
      option.textContent = contextName;
      select.append(option);
    });
    if (result.active) select.value = result.active;
    help.textContent = result.active
      ? `Active context: ${result.active}. Choose another context to switch clusters.`
      : "Choose the cluster context Crelan should use.";
  } catch (cause) {
    select.replaceChildren(new Option("Could not load kubeconfig contexts", ""));
    error.textContent = cause.message;
    error.hidden = false;
  }
}

$("#cluster-connect-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const error = $("#cluster-connect-error");
  const button = $("#connect-cluster");
  const contextName = $("#cluster-context").value;
  if (!contextName) {
    error.textContent = "Choose an available cluster.";
    error.hidden = false;
    return;
  }
  error.hidden = true;
  button.disabled = true;
  button.textContent = "Connecting…";
  try {
    const result = await api("/api/v1/cluster/connect", {
      method: "POST",
      body: JSON.stringify({ context: contextName }),
    });
    $("#cluster-status").textContent = `Connected: ${result.context}`;
    await loadClusterResources();
    await loadClusterContexts();
  } catch (cause) {
    error.textContent = cause.message;
    error.hidden = false;
  } finally {
    button.disabled = false;
    button.textContent = "Use cluster";
  }
});

async function loadDeployments() {
  const list = $("#deployment-list");
  $("#deployment-status").textContent = "Loading deployments…";
  try {
    const deployments = await api("/api/v1/deployments");
    list.replaceChildren();
    if (!deployments.length) {
      const empty = document.createElement("div");
      empty.className = "token-empty";
      empty.textContent = "No deployments yet. Deploy a public repository to get started.";
      list.append(empty);
    }
    deployments.forEach((item) => {
      const row = document.createElement("div");
      row.className = "token-row deployment-row";
      const details = document.createElement("div");
      details.className = "token-row-details";
      const name = document.createElement("strong");
      name.textContent = item.name;
      const repo = document.createElement("code");
      repo.textContent = item.repository_url;
      const cluster = document.createElement("small");
      cluster.className = "deployment-cluster-name";
      cluster.textContent = `Cluster: ${item.cluster_context || "unknown"}`;
      details.append(name, repo, cluster);
      const status = document.createElement("span");
      status.className = `token-row-state ${item.status === "ready" ? "active" : ""}`;
      status.textContent = item.status;
      const remove = document.createElement("button");
      remove.className = "inline-action";
      remove.type = "button";
      remove.textContent = "Delete";
      remove.disabled = ["queued", "building", "deploying", "deleting"].includes(item.status);
      remove.addEventListener("click", () => deleteDeployment(item));
      row.append(details, status, remove);
      if (item.url) {
        const link = document.createElement("a");
        link.className = "deployment-link";
        link.href = item.url;
        link.target = "_blank";
        link.rel = "noopener noreferrer";
        link.textContent = "Open app";
        row.append(link);
      }
      if (item.status_message) {
        const message = document.createElement("small");
        message.className = "deployment-message";
        message.textContent = item.status_message;
        row.append(message);
      }
      list.append(row);
    });
    $("#deployment-status").textContent = "Deployment API connected";
  } catch (cause) {
    list.replaceChildren();
    const failure = document.createElement("p");
    failure.className = "resource-empty";
    failure.textContent = cause.message;
    list.append(failure);
    $("#deployment-status").textContent = "Deployment API unavailable";
  }
}

async function deleteDeployment(item) {
  if (!window.confirm(`Delete deployment “${item.name}”?`)) return;
  try {
    await api(`/api/v1/deployments/${item.id}`, { method: "DELETE" });
    await Promise.all([loadDeployments(), loadClusterResources()]);
  } catch (cause) {
    window.alert(cause.message);
  }
}

$("#deployment-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  const error = $("#deployment-error");
  const submit = $("#deploy-submit");
  error.hidden = true;
  submit.disabled = true;
  submit.textContent = "Queueing deployment…";
  const payload = Object.fromEntries(new FormData(form));
  payload.port = Number(payload.port);
  if (!payload.branch) delete payload.branch;
  try {
    await api("/api/v1/deployments", { method: "POST", body: JSON.stringify(payload) });
    form.reset();
    $("#app-port").value = "8080";
    await loadDeployments();
  } catch (cause) {
    error.textContent = cause.message;
    error.hidden = false;
  } finally {
    submit.disabled = false;
    submit.textContent = "Deploy to cluster";
  }
});

function navigate(hash) {
  const deployments = hash === "#deployments";
  $("#cluster-page").hidden = deployments;
  $("#deployments-page").hidden = !deployments;
  $("#page-title").textContent = deployments ? "Deployments" : "Cluster resources";
  document.querySelectorAll(".nav-link").forEach((link) => link.classList.toggle("active", link.hash === (deployments ? "#deployments" : "#cluster")));
  if (deployments) loadDeployments();
  else loadClusterResources();
}

document.querySelectorAll(".nav-link").forEach((link) => link.addEventListener("click", () => navigate(link.hash)));
window.addEventListener("hashchange", () => navigate(window.location.hash));
$("#refresh-cluster").addEventListener("click", loadClusterResources);
$("#refresh-contexts").addEventListener("click", loadClusterContexts);
$("#refresh-deployments").addEventListener("click", loadDeployments);
$("#signout-button").addEventListener("click", async () => {
  await api("/auth/logout", { method: "POST" });
  window.location.assign("/login");
});

loadAccount().catch((cause) => {
  if (cause.message !== "Sign in required") window.location.assign("/login");
});
loadClusterContexts();
loadClusterResources();
