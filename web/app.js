const $ = (selector) => document.querySelector(selector);
let currentWorkspaceRole = "viewer";

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
  currentWorkspaceRole = user.workspace_role || "viewer";
  const displayName = user.name || user.email || "User";
  $("#account-name").textContent = displayName;
  $("#account-email").textContent = user.email || "";
  $("#top-avatar").textContent = displayName.charAt(0).toUpperCase();
  $(".user-avatar").textContent = displayName.charAt(0).toUpperCase();
  if (user.platform_admin) {
    const adminLink = $("#admin-console-shortcut");
    if (adminLink) {
      adminLink.hidden = false;
      adminLink.style.display = "inline-block";
    }
  }
  const workspaceSelect = $("#workspace-select");
  const membershipResult = await api("/api/v1/workspaces");
  workspaceSelect.replaceChildren();
  membershipResult.workspaces.forEach((workspace) => {
    const option = document.createElement("option");
    option.value = String(workspace.id);
    option.textContent = workspace.name;
    option.selected = workspace.selected;
    workspaceSelect.append(option);
  });
  workspaceSelect.addEventListener("change", async () => {
    await api("/api/v1/workspaces/switch", { method: "POST", body: JSON.stringify({ workspace_id: Number(workspaceSelect.value) }) });
    window.location.reload();
  });
  $("#invite-form").hidden = user.workspace_role !== "owner";
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

async function loadClusterContexts() {
  const select = $("#app-cluster");
  const noClusterAlert = $("#no-cluster-alert");
  if (!select) return;
  try {
    const data = await api("/api/v1/cluster/contexts");
    const contexts = data?.contexts || [];
    const active = data?.active || "";
    const inCluster = !!data?.in_cluster;
    const hasCluster = !!active || contexts.length > 0 || inCluster;
    if (noClusterAlert) noClusterAlert.hidden = hasCluster;

    select.replaceChildren();

    const defaultOption = document.createElement("option");
    defaultOption.value = "";
    defaultOption.textContent = active
      ? `Primary: ${active} (Default)`
      : (hasCluster ? "Default cluster (platform managed)" : "⚠️ No cluster connected (connect in /admin)");
    select.append(defaultOption);

    contexts.forEach((ctx) => {
      if (ctx && ctx !== active) {
        const opt = document.createElement("option");
        opt.value = ctx;
        opt.textContent = ctx;
        select.append(opt);
      }
    });
  } catch (err) {
    console.debug("loadClusterContexts:", err);
  }
}

async function checkGitHubInstallation() {
  const badge = $("#github-connected-badge");
  const connectBtn = $("#github-connect-btn");
  const accountName = $("#github-account-name");
  const autoDeployHintText = $("#github-app-status-text");
  const autoDeployHintLink = $("#github-app-connect-link");
  if (!badge && !connectBtn) return;

  try {
    const data = await api("/api/v1/github/installation");
    if (data?.connected && data?.account) {
      if (accountName) accountName.textContent = "@" + data.account;
      if (badge) badge.hidden = false;
      if (connectBtn) connectBtn.hidden = true;
      if (autoDeployHintText) {
        autoDeployHintText.textContent = `✓ Auto-deploy active: GitHub connected (@${data.account}). Pushes to this branch will automatically trigger rolling deployments.`;
        autoDeployHintText.style.color = "#167044";
      }
      if (autoDeployHintLink) autoDeployHintLink.hidden = true;
    } else {
      if (badge) badge.hidden = true;
      if (connectBtn) connectBtn.hidden = false;
      if (autoDeployHintText) {
        autoDeployHintText.textContent = "💡 Connect GitHub to automatically trigger zero-downtime deployments on git push.";
        autoDeployHintText.style.color = "#667085";
      }
      if (autoDeployHintLink) autoDeployHintLink.hidden = false;
    }
  } catch (err) {
    if (badge) badge.hidden = true;
    if (connectBtn) connectBtn.hidden = false;
  }
}

async function loadDeployments() {
  const list = $("#deployment-list");
  checkGitHubInstallation();
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
      const source = document.createElement("code");
      source.textContent = item.image ? `Image: ${item.image}` : (item.repository_url || "Container application");
      details.append(name, source);
      if (item.cluster_context) {
        const clusterTag = document.createElement("small");
        clusterTag.style.cssText = "display: block; color: #667085; font-size: 10px; margin-top: 2px;";
        clusterTag.textContent = `Target: ${item.cluster_context}`;
        details.append(clusterTag);
      }
      const status = document.createElement("span");
      status.className = `token-row-state ${["ready", "running"].includes(item.status) ? "active" : ""}`;
      status.textContent = item.status;
      const remove = document.createElement("button");
      remove.className = "inline-action";
      remove.type = "button";
      remove.textContent = "Delete";
      remove.disabled = ["queued", "building", "deploying", "deleting"].includes(item.status);
      remove.addEventListener("click", () => deleteDeployment(item));
      const inspect = document.createElement("button");
      inspect.className = "inline-action";
      inspect.type = "button";
      inspect.textContent = "View app";
      inspect.addEventListener("click", () => showApplication(item));
      row.append(details, status, inspect, remove);
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
    $("#deployment-status").textContent = "Workspace applications loaded";
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
  const removesVolume = Boolean(item.persistent_storage || item.volume || item.pvc);
  const prompt = removesVolume
    ? `Delete deployment “${item.name}”? Its persistent volume will be retained. You can purge it later from Retained volumes.`
    : `Delete deployment “${item.name}”?`;
  if (!window.confirm(prompt)) return;
  try {
    await api(`/api/v1/deployments/${item.id}`, { method: "DELETE" });
    if (activeApplication?.id === item.id) closeApplication();
    await loadDeployments();
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
  const sourceType = payload.source_type;
  delete payload.source_type;
  if (!payload.cluster_context) {
    delete payload.cluster_context;
  }
  if (sourceType === "image") {
    payload.port = Number(payload.image_port);
    delete payload.image_port;
    if ($("#image-pull-secret").value) payload.image_pull_secret = $("#image-pull-secret").value;
    delete payload.repository_url;
    delete payload.branch;
    if (!payload.image) {
      error.textContent = "Enter a container image reference.";
      error.hidden = false;
      submit.disabled = false;
      submit.textContent = "Deploy application";
      return;
    }
  } else {
    payload.port = Number(payload.port);
    delete payload.image;
    delete payload.image_port;
    if (!payload.branch) delete payload.branch;
    if (!payload.repository_url) {
      error.textContent = "Enter a GitHub repository URL.";
      error.hidden = false;
      submit.disabled = false;
      submit.textContent = "Deploy application";
      return;
    }
    const dockerfile = $("#app-dockerfile")?.value.trim();
    if (dockerfile) payload.dockerfile = dockerfile;
    const buildStrategy = $("#app-build-strategy")?.value;
    if (buildStrategy && buildStrategy !== "auto") payload.build_strategy = buildStrategy;
    const gitToken = $("#app-git-token")?.value.trim();
    if (gitToken) payload.git_token = gitToken;
  }
  if ($("#app-volume-enabled").checked) {
    payload.persistent_storage = { size: $("#app-volume-size").value, mount_path: $("#app-volume-mount").value.trim(), access_mode: $("#app-volume-access-mode").value || "single_writer" };
    if (!payload.persistent_storage.mount_path) {
      error.textContent = "Enter a mount path for persistent storage.";
      error.hidden = false;
      submit.disabled = false;
      submit.textContent = "Deploy application";
      return;
    }
  } else {
    delete payload.persistent_storage;
  }
  const initialEnvRaw = $("#deploy-initial-env")?.value || "";
  try {
    const created = await api("/api/v1/deployments", { method: "POST", body: JSON.stringify(payload) });
    if (initialEnvRaw.trim() && created?.id) {
      const initialEnvMap = {};
      for (const rawLine of initialEnvRaw.split("\n")) {
        const line = rawLine.trim();
        if (!line || line.startsWith("#")) continue;
        const eqIdx = line.indexOf("=");
        if (eqIdx > 0) {
          const k = line.slice(0, eqIdx).trim();
          const v = line.slice(eqIdx + 1).trim();
          if (k) initialEnvMap[k] = v;
        }
      }
      if (Object.keys(initialEnvMap).length > 0) {
        try {
          await api(`/api/v1/deployments/${encodeURIComponent(created.id)}/configuration`, {
            method: "PUT",
            body: JSON.stringify({ environment: initialEnvMap, secrets: {} }),
          });
        } catch (envErr) {
          console.warn("Could not apply initial environment variables:", envErr);
        }
      }
    }
    form.reset();
    $("#app-port").value = "8080";
    $("#image-port").value = "8080";
    updateSourceFields();
    updateVolumeFields();
    window.location.hash = "applications";
    await loadDeployments();
  } catch (cause) {
    if (String(cause.message || "").includes("not connected or unavailable")) {
      error.innerHTML = `⚠️ ${cause.message}. Reconnect your cluster in the <a href="/admin" style="color: #c45100; text-decoration: underline; font-weight: 600;">Admin Console</a>, or configure <code>CRELAN_KUBECONFIG_DATA_B64</code> in your environment variables.`;
    } else {
      error.textContent = cause.message;
    }
    error.hidden = false;
  } finally {
    submit.disabled = false;
    submit.textContent = "Deploy application";
  }
});

let activeApplication = null;

async function showApplication(item) {
  activeApplication = item;
  $("#app-runtime").hidden = false;
  $("#runtime-app-name").textContent = item.name;
  $("#runtime-app-summary").textContent = item.image ? `Image: ${item.image}` : (item.repository_url || "Application runtime details");
  const errBox = $("#runtime-error");
  if (item.status === "failed") {
    errBox.innerHTML = `<strong>Deployment Failed:</strong> ${escapeHTML(item.status_message || "The build or container rollout failed. Inspect logs below for details.")}`;
    errBox.hidden = false;
  } else {
    errBox.hidden = true;
  }
  await Promise.all([
    loadApplicationResources(),
    loadApplicationLogs(),
    loadApplicationEvents(),
    loadConnections(),
    loadCustomDomains(),
  ]);
}

function closeApplication() {
  activeApplication = null;
  $("#app-runtime").hidden = true;
}

async function loadApplicationResources() {
  if (!activeApplication) return;
  const error = $("#runtime-error");
  if (activeApplication.status !== "failed") {
    error.hidden = true;
  }
  try {
    const result = await api(`/api/v1/deployments/${activeApplication.id}/resources`);
    const instances = result.instances || [];
    const config = result.configuration || [];
    const secrets = result.secret_metadata || [];
    $("#app-pod-count").textContent = instances.length;
    renderTable($("#app-pods-list"), [
      { label: "Instance", value: (pod) => pod.name },
      { label: "Status", value: (pod) => pod.phase || "Pending" },
      { label: "Ready", value: (pod) => pod.ready ? "Yes" : "No" },
      { label: "Restarts", value: (pod) => pod.restarts },
    ], instances, "No application instances are available yet.");
    const configurationRows = [
      ...config.map((item) => ({ name: item.name, kind: "ConfigMap", keys: item.keys })),
      ...secrets.map((item) => ({ name: item.name, kind: "Secret metadata", keys: item.keys })),
    ];
    renderTable($("#app-config-list"), [
      { label: "Name", value: (item) => item.name },
      { label: "Type", value: (item) => item.kind },
      { label: "Keys", value: (item) => item.keys },
    ], configurationRows, "No app configuration metadata is available.");
  } catch (cause) {
    if (activeApplication.status !== "failed") {
      error.textContent = cause.message;
      error.hidden = false;
    }
  }
}

async function loadApplicationLogs() {
  if (!activeApplication) return;
  const output = $("#app-logs");
  output.textContent = "Loading logs…";
  try {
    const result = await api(`/api/v1/deployments/${activeApplication.id}/logs?tail=200`);
    output.textContent = result.logs || (activeApplication.status === "failed" ? `Deployment failed:\n${activeApplication.status_message}` : "No log output is available.");
  } catch (cause) {
    if (activeApplication.status === "failed") {
      output.textContent = `Deployment Failed:\n${activeApplication.status_message || cause.message}`;
    } else {
      output.textContent = cause.message;
    }
  }
}

async function loadApplicationEvents() {
  if (!activeApplication) return;
  try {
    const result = await api(`/api/v1/deployments/${encodeURIComponent(activeApplication.id)}/events`);
    renderTable($("#app-events-list"), [
      { label: "Time", value: (event) => event.last_seen ? new Date(event.last_seen).toLocaleString() : "—" },
      { label: "Reason", value: (event) => event.reason },
      { label: "Resource", value: (event) => event.object },
      { label: "Count", value: (event) => event.count },
      { label: "Details", value: (event) => event.message },
    ], result.events || [], "No recent warning events for this application.");
  } catch (cause) {
    $("#app-events-list").textContent = cause.message;
  }
}

async function loadDatabases() {
  const list = $("#database-list");
  list.replaceChildren();
  try {
    const result = await api("/api/v1/databases");
    for (const item of result.databases || []) {
      const row = document.createElement("div");
      row.className = "token-row deployment-row";
      const details = document.createElement("div");
      details.className = "token-row-details";
      const name = document.createElement("strong");
      name.textContent = item.name;
      const endpoint = document.createElement("code");
      endpoint.textContent = `${item.endpoint}:${item.port}`;
      const engine = document.createElement("small");
      engine.textContent = `${item.engine} ${item.version || ""} · ${item.storage || ""} · backups ${item.backups_enabled ? "enabled" : "not configured"}`;
      details.append(name, endpoint, engine);
      const status = document.createElement("span");
      status.className = `token-row-state ${["healthy", "ready", "online"].includes(String(item.status).toLowerCase()) ? "active" : ""}`;
      status.textContent = item.status || "provisioning";
      const remove = document.createElement("button");
      remove.className = "inline-action";
      remove.type = "button";
      remove.textContent = "Delete";
      remove.addEventListener("click", async () => {
        if (!window.confirm(`Delete database cluster “${item.name}”? This also deletes its stored data.`)) return;
        try {
          await api(`/api/v1/databases/${encodeURIComponent(item.name)}`, { method: "DELETE" });
          await loadDatabases();
        } catch (cause) {
          window.alert(cause.message);
        }
      });
      row.append(details, status);
      if (item.backups_enabled) {
        const backups = document.createElement("button");
        backups.className = "inline-action";
        backups.type = "button";
        backups.textContent = "Backups";
        backups.addEventListener("click", () => manageDatabaseBackups(item));
        row.append(backups);
      }
      row.append(remove);
      if (item.message) {
        const message = document.createElement("small");
        message.className = "deployment-message";
        message.textContent = item.message;
        row.append(message);
      }
      list.append(row);
    }
    if (!list.children.length) {
      const empty = document.createElement("div");
      empty.className = "token-empty";
      empty.textContent = "No databases yet. Create a PostgreSQL or MySQL cluster to get started.";
      list.append(empty);
    }
  } catch (cause) {
    const failure = document.createElement("p");
    failure.className = "resource-empty";
    failure.textContent = cause.message;
    list.append(failure);
  }
}

async function manageDatabaseBackups(database) {
  try {
    const result = await api(`/api/v1/databases/${encodeURIComponent(database.name)}/backups`);
    const backups = result.backups || [];
    const summary = backups.length ? backups.map((backup) => `${backup.id}: ${backup.status || "pending"}${backup.started_at ? ` (${backup.started_at})` : ""}`).join("\n") : "No backup records yet.";
    window.alert(`${database.name} backups\n\n${summary}`);
    if (result.enabled && window.confirm("Start a new on-demand backup?")) {
      await api(`/api/v1/databases/${encodeURIComponent(database.name)}/backups`, { method: "POST", body: "{}" });
      window.alert("Backup queued. Reopen Backups to refresh its status.");
      return;
    }
    if (result.restore_supported && backups.some((backup) => String(backup.status).toLowerCase() === "completed") && window.confirm("Restore the latest completed backup as a new PostgreSQL database? The source will remain unchanged.")) {
      const name = window.prompt("New database name for the restored copy:");
      if (!name) return;
      await api(`/api/v1/databases/${encodeURIComponent(database.name)}/restore`, { method: "POST", body: JSON.stringify({ name }) });
      window.alert("Restore queued as a new database. Refresh the database list to track it.");
      await loadDatabases();
    }
  } catch (cause) {
    window.alert(cause.message);
  }
}

async function loadWorkspaceStorage() {
  const list = $("#workspace-storage-list");
  list.replaceChildren();
  try {
    const result = await api("/api/v1/workspace/storage");
    for (const volume of result.volumes || []) {
      const row = document.createElement("div");
      row.className = "token-row service-resource-row";
      const details = document.createElement("div");
      details.className = "token-row-details";
      const name = document.createElement("strong");
      name.textContent = volume.application;
      const pvc = document.createElement("code");
      pvc.textContent = `${volume.name} · ${volume.size || "size unknown"} · ${volume.access_mode}${volume.mount_path ? ` · mounted at ${volume.mount_path}` : ""}`;
      const status = document.createElement("small");
      status.textContent = `${volume.status}${volume.orphaned ? " · retained after app deletion" : " · app still exists"}`;
      details.append(name, pvc, status);
      row.append(details);
      if (volume.orphaned) {
        const purge = document.createElement("button");
        purge.className = "inline-action";
        purge.type = "button";
        purge.textContent = "Purge data";
        purge.addEventListener("click", async () => {
          if (!window.confirm(`Permanently delete ${volume.name} and all data for ${volume.application}?`)) return;
          try {
            await api(`/api/v1/workspace/storage/${encodeURIComponent(volume.application)}?confirm_data_loss=true`, { method: "DELETE" });
            await loadWorkspaceStorage();
          } catch (cause) { window.alert(cause.message); }
        });
        row.append(purge);
      }
      list.append(row);
    }
    if (!list.children.length) list.innerHTML = '<div class="token-empty">No persistent app volumes found.</div>';
  } catch (cause) {
    list.textContent = cause.message;
  }
}

async function loadDatabaseEngines() {
  try {
    const result = await api("/api/v1/database-engines");
    const select = $("#database-engine");
    for (const engine of result.engines || []) {
      const option = [...select.options].find((item) => item.value === engine.id);
      if (option) {
        option.disabled = !engine.available;
        option.textContent = `${engine.name}${!engine.available ? " (operator not installed)" : (engine.backups_available ? " (backups enabled)" : " (backups unavailable)")}`;
      }
    }
    $("#database-backup-note").textContent = "Backups require an operator schema and S3-compatible bucket configured by the platform. PostgreSQL restore creates a new database from the latest completed backup; MySQL restore is not currently exposed.";
  } catch {
    // Keep the catalog visible; create requests return the specific provisioning issue.
  }
}

let serviceCatalog = [];
let editingConnectionId = null;
let editingConnectionSecretKeys = [];
let workspaceConnections = [];
let oneTimeManagedServiceCredentials = null;

async function loadServiceCatalog() {
  const select = $("#managed-service-type");
  const versionSelect = $("#managed-service-version");
  const error = $("#managed-service-error");
  try {
    const result = await api("/api/v1/service-catalog");
    serviceCatalog = result.services || [];
    select.replaceChildren();
    const available = serviceCatalog.filter((service) => service.available !== false);
    if (!available.length) {
      select.append(new Option("No managed services available", ""));
      select.disabled = true;
      $("#managed-service-submit").disabled = true;
      error.textContent = "No supported service operator is currently available for this cluster.";
      error.hidden = false;
      return;
    }
    for (const service of serviceCatalog) {
      const value = service.type || service.id;
      const label = service.name || value;
      const displayLabel = value === "valkey" ? `${label} (experimental preview)` : label;
      const option = new Option(service.available === false ? `${displayLabel} (unavailable)` : displayLabel, value);
      option.disabled = service.available === false;
      select.append(option);
    }
    select.disabled = false;
    $("#managed-service-submit").disabled = false;
    error.hidden = true;
    const renderVersions = () => {
      const selected = serviceCatalog.find((item) => (item.type || item.id) === select.value);
      $("#valkey-preview-note").hidden = select.value !== "valkey";
      $("#managed-service-description").textContent = selected?.description || "";
      versionSelect.replaceChildren(new Option("Use catalog default", ""));
      (selected?.versions || []).forEach((version) => versionSelect.append(new Option(String(version) === "operator-default" ? "Operator default" : String(version), String(version))));
      if (selected?.default_version) versionSelect.value = String(selected.default_version);
      const storage = $("#managed-service-storage");
      storage.replaceChildren(new Option("Use platform default", ""));
      (selected?.storage_options || []).forEach((size) => storage.append(new Option(`${size} per node`, String(size))));
      storage.disabled = selected?.persistent_storage === false;
      if (storage.disabled) storage.value = "";
    };
    select.onchange = renderVersions;
    renderVersions();
  } catch (cause) {
    select.replaceChildren(new Option("Service catalog unavailable", ""));
    select.disabled = true;
    $("#managed-service-submit").disabled = true;
    error.textContent = cause.message;
    error.hidden = false;
  }
}

async function loadManagedServices() {
  const list = $("#managed-service-list");
  list.replaceChildren();
  try {
    const result = await api("/api/v1/services");
    for (const item of result.services || []) {
      const row = document.createElement("div");
      row.className = "token-row service-resource-row";
      const details = document.createElement("div");
      details.className = "token-row-details";
      const name = document.createElement("strong");
      name.textContent = item.name;
      const endpoint = document.createElement("code");
      endpoint.textContent = item.endpoint ? `${item.endpoint}${item.port ? `:${item.port}` : ""}` : `${item.type} · ${item.version || "default version"}`;
      const meta = document.createElement("small");
      const backupStatus = item.backup?.supported ? `backups ${item.backup.status || "supported"}` : "backups not configured";
      meta.textContent = `${item.type === "valkey" ? "Experimental preview · " : ""}${item.type}${item.version ? ` ${item.version}` : ""}${item.storage ? ` · ${item.storage}/node` : ""} · ${backupStatus}${item.production_ready ? " · production ready" : " · production readiness not established"}`;
      details.append(name, endpoint, meta);
      const status = document.createElement("span");
      status.className = `token-row-state ${["ready", "healthy", "running"].includes(String(item.status).toLowerCase()) ? "active" : ""}`;
      status.textContent = item.status || "provisioning";
      const remove = document.createElement("button");
      remove.className = "inline-action";
      remove.type = "button";
      remove.textContent = "Delete";
      remove.addEventListener("click", async () => {
        const confirmation = item.storage
          ? `Delete managed service “${item.name}” and its persistent data (${item.storage} per node)?`
          : `Delete managed service “${item.name}”?`;
        if (!window.confirm(confirmation)) return;
        try {
          await api(`/api/v1/services/${encodeURIComponent(item.id)}`, { method: "DELETE" });
          await loadManagedServices();
        } catch (cause) { window.alert(cause.message); }
      });
      row.append(details, status, remove);
      if (item.message) {
        const message = document.createElement("small");
        message.className = "deployment-message";
        message.textContent = item.message;
        row.append(message);
      }
      list.append(row);
    }
    if (!list.children.length) list.innerHTML = '<div class="token-empty">No managed services yet.</div>';
  } catch (cause) {
    const failure = document.createElement("p");
    failure.className = "resource-empty";
    failure.textContent = cause.message;
    list.append(failure);
  }
}

async function loadManagedServiceConnectionSources() {
  const select = $("#managed-connection-source");
  const error = $("#managed-connection-error");
  error.hidden = true;
  select.replaceChildren(new Option("Loading managed resources…", ""));
  select.disabled = true;
  try {
    const [serviceResult, databaseResult] = await Promise.all([
      api("/api/v1/services"),
      api("/api/v1/databases"),
    ]);
    const sources = [
      ...(databaseResult.databases || []).map((item) => ({ ...item, type: item.engine || item.type })),
      ...(serviceResult.services || []),
    ].filter((item) => ["postgresql", "mysql", "valkey", "rabbitmq"].includes(item.type || item.engine));
    select.replaceChildren(new Option(sources.length ? "Choose a managed resource" : "No managed resources found", ""));
    sources.forEach((item) => {
      const type = item.type || item.engine;
      select.append(new Option(`${item.name} · ${type}${item.status ? ` · ${item.status}` : ""}`, `${type}:${item.name}`));
    });
    select.disabled = sources.length === 0;
    $("#managed-connection-submit").disabled = true;
    if (!sources.length) {
      error.textContent = "Create a PostgreSQL/MySQL database or Valkey/RabbitMQ service first.";
      error.hidden = false;
    }
  } catch (cause) {
    select.replaceChildren(new Option("Could not load managed resources", ""));
    select.disabled = true;
    $("#managed-connection-submit").disabled = true;
    $("#managed-connection-error").textContent = cause.message;
    $("#managed-connection-error").hidden = false;
  }
}
$("#managed-connection-source").addEventListener("change", () => {
  $("#managed-connection-submit").disabled = !$("#managed-connection-source").value;
});

$("#managed-connection-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const error = $("#managed-connection-error");
  const button = $("#managed-connection-submit");
  error.hidden = true;
  button.disabled = true;
  try {
    await api("/api/v1/workspace/connections/from-managed-service", {
      method: "POST",
      body: JSON.stringify({ name: $("#managed-connection-name").value.trim(), service_id: $("#managed-connection-source").value }),
    });
    event.currentTarget.reset();
    await loadConnections();
    await loadManagedServiceConnectionSources();
  } catch (cause) {
    error.textContent = cause.message;
    error.hidden = false;
  } finally {
    button.disabled = $("#managed-connection-source").disabled || !$("#managed-connection-source").value;
  }
});

$("#managed-service-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const error = $("#managed-service-error");
  const submit = $("#managed-service-submit");
  error.hidden = true;
  submit.disabled = true;
  submit.textContent = "Creating service…";
  const payload = Object.fromEntries(new FormData(event.currentTarget));
  if (!payload.version) delete payload.version;
  if (!payload.storage) delete payload.storage;
  try {
    const result = await api("/api/v1/services", { method: "POST", body: JSON.stringify(payload) });
    if (result.credentials) {
      oneTimeManagedServiceCredentials = result.credentials;
      $("#managed-service-credential-values").textContent = JSON.stringify(result.credentials, null, 2);
      $("#managed-service-credentials").hidden = false;
    }
    event.currentTarget.reset();
    await loadManagedServices();
  } catch (cause) {
    error.textContent = cause.message;
    error.hidden = false;
  } finally {
    submit.disabled = false;
    submit.textContent = "Create service";
  }
});

$("#copy-managed-service-credentials").addEventListener("click", async () => {
  if (!oneTimeManagedServiceCredentials) return;
  try {
    await navigator.clipboard.writeText($("#managed-service-credential-values").textContent);
    $("#copy-managed-service-credentials").textContent = "Copied";
  } catch {
    $("#copy-managed-service-credentials").textContent = "Select and copy the credentials above";
  }
});

function updateConnectionFields() {
  const objectStorage = $("#connection-type").value === "object_storage";
  const hasConnectionURL = !objectStorage && $("#connection-url").value.trim() !== "";
  const expectsConnectionURL = editingConnectionId && editingConnectionSecretKeys.includes("CONNECTION_URL");
  $("#connection-standard-fields").hidden = objectStorage;
  $("#connection-object-storage-fields").hidden = !objectStorage;
  $("#connection-url").required = Boolean(expectsConnectionURL && !hasConnectionURL);
  $("#connection-host").required = !objectStorage && !hasConnectionURL && !expectsConnectionURL;
  $("#connection-port").required = !objectStorage && !hasConnectionURL && !expectsConnectionURL;
  $("#connection-endpoint").required = objectStorage;
  $("#connection-bucket").required = objectStorage;
  $("#connection-access-key").required = objectStorage && (!editingConnectionId || editingConnectionSecretKeys.includes("ACCESS_KEY_ID"));
  $("#connection-secret-key").required = objectStorage && (!editingConnectionId || editingConnectionSecretKeys.includes("SECRET_ACCESS_KEY"));
  $("#connection-password").required = !objectStorage && editingConnectionSecretKeys.includes("PASSWORD");
  $("#connection-username").required = !objectStorage && editingConnectionSecretKeys.includes("USERNAME");
  $("#connection-password").autocomplete = editingConnectionId ? "off" : "new-password";
}

function resetConnectionForm() {
  editingConnectionId = null;
  editingConnectionSecretKeys = [];
  $("#connection-form").reset();
  $("#connection-form-heading").textContent = "Add a connection";
  $("#connection-submit").textContent = "Save connection";
  $("#connection-cancel-edit").hidden = true;
  $("#connection-edit-secret-note").hidden = true;
  $("#connection-name").disabled = false;
  $("#connection-type").disabled = false;
  $("#connection-secret-key").value = "";
  $("#connection-password").value = "";
  updateConnectionFields();
}

async function loadConnections() {
  const list = $("#connection-list");
  list.replaceChildren();
  try {
    const result = await api("/api/v1/workspace/connections");
    workspaceConnections = result.connections || [];
    const attachSelect = $("#app-connection-select");
    attachSelect.replaceChildren(new Option("Choose a saved connection", ""));
    workspaceConnections.forEach((item) => attachSelect.append(new Option(`${item.name} · ${item.type}`, item.id)));
    attachSelect.disabled = workspaceConnections.length === 0;
    $("#app-connection-submit").disabled = true;
    renderConnectionMappings();
    for (const item of workspaceConnections) {
      const row = document.createElement("div");
      row.className = "token-row service-resource-row";
      const details = document.createElement("div");
      details.className = "token-row-details";
      const name = document.createElement("strong");
      name.textContent = item.name;
      const endpoint = document.createElement("code");
      endpoint.textContent = item.type === "object_storage"
        ? `${item.endpoint || "S3 endpoint unavailable"} · bucket ${item.bucket || "—"}`
        : `${item.host || "—"}${item.port ? `:${item.port}` : ""}${item.database ? ` · ${item.database}` : ""}`;
      const meta = document.createElement("small");
      meta.textContent = item.type === "object_storage" ? `S3-compatible · ${item.region || "region not set"}` : `${item.type} · credentials saved`;
      details.append(name, endpoint, meta);
      const edit = document.createElement("button");
      edit.className = "inline-action";
      edit.type = "button";
      edit.textContent = "Edit";
      edit.addEventListener("click", () => {
        $("#connection-rollout-status").textContent = "";
        editingConnectionId = item.id;
        editingConnectionSecretKeys = item.secret_keys || [];
        $("#connection-form").reset();
        $("#connection-name").value = item.name || "";
        $("#connection-type").value = item.type || "other";
        $("#connection-url").value = "";
        $("#connection-host").value = item.host || "";
        $("#connection-port").value = item.port || "";
        $("#connection-database").value = item.database || "";
        $("#connection-username").value = item.username || "";
        $("#connection-endpoint").value = item.endpoint || "";
        $("#connection-bucket").value = item.bucket || "";
        $("#connection-region").value = item.region || "";
        $("#connection-access-key").value = "";
        $("#connection-password").value = "";
        $("#connection-secret-key").value = "";
        $("#connection-form-heading").textContent = `Edit ${item.name}`;
        $("#connection-submit").textContent = "Save changes";
        $("#connection-cancel-edit").hidden = false;
        $("#connection-name").disabled = true;
        $("#connection-type").disabled = true;
        $("#connection-edit-secret-note").hidden = editingConnectionSecretKeys.length === 0;
        updateConnectionFields();
        $("#connection-form").scrollIntoView({ behavior: "smooth", block: "center" });
      });
      const remove = document.createElement("button");
      remove.className = "inline-action";
      remove.type = "button";
      remove.textContent = "Delete";
      remove.addEventListener("click", async () => {
        if (!window.confirm(`Delete connection “${item.name}”?`)) return;
        try {
          await api(`/api/v1/workspace/connections/${encodeURIComponent(item.id)}`, { method: "DELETE" });
          if (editingConnectionId === item.id) resetConnectionForm();
          await loadConnections();
        } catch (cause) { window.alert(cause.message); }
      });
      row.append(details, edit, remove);
      list.append(row);
    }
    if (!list.children.length) list.innerHTML = '<div class="token-empty">No external connections saved yet.</div>';
  } catch (cause) {
    const failure = document.createElement("p");
    failure.className = "resource-empty";
    failure.textContent = cause.message;
    list.append(failure);
    const attachSelect = $("#app-connection-select");
    attachSelect.replaceChildren(new Option("Could not load saved connections", ""));
    attachSelect.disabled = true;
    $("#app-connection-submit").disabled = true;
  }
}

function suggestedEnvName(connectionType, secretKey) {
  const type = String(connectionType || "other").toLowerCase();
  const key = String(secretKey || "").toUpperCase();
  const known = {
    CONNECTION_URL: { postgresql: "DB_CONNECTION_URI", mysql: "DB_CONNECTION_URI", valkey: "REDIS_URL", redis: "REDIS_URL", rabbitmq: "AMQP_URL" },
    HOST: { postgresql: "DB_HOST", mysql: "DB_HOST", valkey: "REDIS_HOST", redis: "REDIS_HOST", rabbitmq: "AMQP_HOST" },
    PORT: { postgresql: "DB_PORT", mysql: "DB_PORT", valkey: "REDIS_PORT", redis: "REDIS_PORT", rabbitmq: "AMQP_PORT" },
    DATABASE: { postgresql: "DB_NAME", mysql: "DB_NAME" },
    USERNAME: { postgresql: "DB_USERNAME", mysql: "DB_USERNAME" },
    PASSWORD: { postgresql: "DB_PASSWORD", mysql: "DB_PASSWORD", valkey: "REDIS_PASSWORD", redis: "REDIS_PASSWORD", rabbitmq: "AMQP_PASSWORD" },
    ENDPOINT: { object_storage: "S3_ENDPOINT" },
    BUCKET: { object_storage: "S3_BUCKET" },
    REGION: { object_storage: "AWS_REGION" },
    ACCESS_KEY_ID: { object_storage: "AWS_ACCESS_KEY_ID" },
    SECRET_ACCESS_KEY: { object_storage: "AWS_SECRET_ACCESS_KEY" },
  };
  return known[key]?.[type] || `${type}_${key}`.replace(/[^A-Z0-9_]/g, "_").toUpperCase();
}

function renderConnectionMappings() {
  const container = $("#app-connection-mappings");
  const connection = workspaceConnections.find((item) => String(item.id) === $("#app-connection-select").value);
  container.replaceChildren();
  if (!connection) {
    $("#app-connection-status").textContent = workspaceConnections.length ? "Choose a connection to configure environment variable mappings." : "Save a connection first, then attach it to this app.";
    $("#app-connection-submit").disabled = true;
    return;
  }
  const keys = connection.secret_keys || [];
  if (!keys.length) {
    $("#app-connection-status").textContent = "This connection has no secret fields available for injection.";
    $("#app-connection-submit").disabled = true;
    return;
  }
  $("#app-connection-status").textContent = "Choose which secret fields to expose and set the environment variable names expected by your app.";
  keys.forEach((key) => {
    const row = document.createElement("div");
    row.className = "connection-mapping-row";
    const variableField = document.createElement("div");
    const variableLabel = document.createElement("label");
    variableLabel.className = "form-label";
    variableLabel.textContent = "App environment variable";
    const variable = document.createElement("input");
    variable.className = "text-input deployment-input";
    variable.dataset.connectionEnv = "true";
    variable.autocapitalize = "characters";
    variable.value = suggestedEnvName(connection.type, key);
    variable.maxLength = 128;
    variable.placeholder = "Optional; clear to skip this field";
    variableLabel.append(variable);
    variableField.append(variableLabel);
    const secretField = document.createElement("div");
    const secretLabel = document.createElement("label");
    secretLabel.className = "form-label";
    secretLabel.textContent = "Stored secret field";
    const secret = document.createElement("select");
    secret.className = "text-input deployment-input";
    secret.dataset.connectionSecret = "true";
    keys.forEach((name) => secret.append(new Option(name, name)));
    secret.value = key;
    secretLabel.append(secret);
    secretField.append(secretLabel);
    row.append(variableField, secretField);
    container.append(row);
  });
  $("#app-connection-submit").disabled = false;
}

$("#app-connection-select").addEventListener("change", renderConnectionMappings);
$("#app-connection-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!activeApplication) return;
  const error = $("#app-connection-error");
  const button = $("#app-connection-submit");
  error.hidden = true;
  const envMapping = {};
  const envInputs = [...document.querySelectorAll("[data-connection-env]")];
  const secretInputs = [...document.querySelectorAll("[data-connection-secret]")];
  for (const [index, input] of envInputs.entries()) {
    const name = input.value.trim();
    if (!name) continue;
    if (!/^[A-Za-z_][A-Za-z0-9_]{0,127}$/.test(name)) {
      error.textContent = `“${name}” is not a valid environment variable name.`;
      error.hidden = false;
      return;
    }
    if (Object.hasOwn(envMapping, name)) {
      error.textContent = `Environment variable “${name}” is mapped more than once.`;
      error.hidden = false;
      return;
    }
    envMapping[name] = secretInputs[index].value;
  }
  if (!Object.keys(envMapping).length) {
    error.textContent = "Add at least one environment variable mapping.";
    error.hidden = false;
    return;
  }
  button.disabled = true;
  try {
    const result = await api(`/api/v1/deployments/${encodeURIComponent(activeApplication.id)}/connections`, {
      method: "POST",
      body: JSON.stringify({ connection_id: $("#app-connection-select").value, env_mapping: envMapping }),
    });
    $("#app-connection-status").textContent = `Attached ${result.environment_keys?.length || Object.keys(envMapping).length} environment variable(s). Crelan passes secret references to Kubernetes; values are not returned here.`;
  } catch (cause) {
    error.textContent = cause.message;
    error.hidden = false;
  } finally {
    button.disabled = false;
  }
});

$("#connection-type").addEventListener("change", updateConnectionFields);
$("#connection-url").addEventListener("input", updateConnectionFields);
updateConnectionFields();
$("#connection-cancel-edit").addEventListener("click", resetConnectionForm);
$("#connection-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const error = $("#connection-error");
  error.hidden = true;
  const objectStorage = $("#connection-type").value === "object_storage";
  const payload = {
    name: $("#connection-name").value.trim(),
    type: $("#connection-type").value,
  };
  if (objectStorage) {
    Object.assign(payload, {
      endpoint: $("#connection-endpoint").value.trim(),
      bucket: $("#connection-bucket").value.trim(),
      region: $("#connection-region").value.trim(),
    });
    if ($("#connection-access-key").value.trim()) payload.access_key_id = $("#connection-access-key").value.trim();
    if ($("#connection-secret-key").value) payload.secret_access_key = $("#connection-secret-key").value;
  } else {
    const connectionURL = $("#connection-url").value.trim();
    if (connectionURL) payload.connection_url = connectionURL;
    else Object.assign(payload, { host: $("#connection-host").value.trim(), port: Number($("#connection-port").value) });
    Object.assign(payload, { database: $("#connection-database").value.trim(), username: $("#connection-username").value.trim() });
    if ($("#connection-password").value) payload.password = $("#connection-password").value;
  }
  const button = $("#connection-submit");
  button.disabled = true;
  $("#connection-rollout-status").textContent = "";
  try {
    const path = editingConnectionId ? `/api/v1/workspace/connections/${encodeURIComponent(editingConnectionId)}` : "/api/v1/workspace/connections";
    const saved = await api(path, { method: editingConnectionId ? "PUT" : "POST", body: JSON.stringify(payload) });
    if (saved.rollout_status) {
      const appNames = (saved.affected_apps || []).join(", ");
      $("#connection-rollout-status").textContent = saved.rollout_error || (saved.rollout_status === "triggered"
        ? `Credential change saved. Restart triggered for: ${appNames || "no attached apps"}.`
        : `Credential change saved. App rollout status: ${saved.rollout_status}.`);
    } else {
      $("#connection-rollout-status").textContent = "Connection saved securely.";
    }
    resetConnectionForm();
    // Keep the rollout result visible after resetting the inputs.
    if (saved.rollout_status) $("#connection-rollout-status").textContent = saved.rollout_error || `Connection saved. App rollout status: ${saved.rollout_status}${saved.affected_apps?.length ? ` (${saved.affected_apps.join(", ")})` : ""}.`;
    await loadConnections();
  } catch (cause) {
    error.textContent = cause.message;
    error.hidden = false;
  } finally {
    button.disabled = false;
  }
});

$("#open-object-storage-connection").addEventListener("click", () => {
  $("#connection-type").value = "object_storage";
  updateConnectionFields();
  $("#connection-name").focus();
  $("#connection-form").scrollIntoView({ behavior: "smooth", block: "center" });
});

async function loadRegistryCredentials() {
  const select = $("#image-pull-secret");
  const selected = select.value;
  select.replaceChildren(new Option("Use public image or platform registry", ""));
  try {
    const result = await api("/api/v1/registry-credentials");
    for (const credential of result.credentials || []) {
      const option = new Option(`${credential.name} (${credential.server})`, credential.name);
      select.append(option);
    }
    if ([...select.options].some((option) => option.value === selected)) select.value = selected;
  } catch {
    // Public images remain deployable when registry credential management is unavailable.
  }
}

$("#show-registry-form").addEventListener("click", () => {
  $("#registry-form").hidden = !$("#registry-form").hidden;
});
$("#registry-submit").addEventListener("click", async () => {
  const error = $("#registry-error");
  const submit = $("#registry-submit");
  error.hidden = true;
  const payload = {
    name: $("#registry-name").value.trim(),
    server: $("#registry-server").value.trim(),
    username: $("#registry-username").value.trim(),
    password: $("#registry-password").value,
    token: $("#registry-password").value,
  };
  submit.disabled = true;
  try {
    const credential = await api("/api/v1/registry-credentials", { method: "POST", body: JSON.stringify(payload) });
    await loadRegistryCredentials();
    $("#image-pull-secret").value = credential.name;
    $("#registry-password").value = "";
    $("#registry-form").hidden = true;
  } catch (cause) {
    error.textContent = cause.message;
    error.hidden = false;
  } finally {
    submit.disabled = false;
  }
});

let oneTimeDatabaseCredentials = null;
$("#database-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const error = $("#database-error");
  const submit = $("#database-submit");
  error.hidden = true;
  submit.disabled = true;
  submit.textContent = "Creating database…";
  const payload = Object.fromEntries(new FormData(event.currentTarget));
  if (!payload.version) delete payload.version;
  try {
    const result = await api("/api/v1/databases", { method: "POST", body: JSON.stringify(payload) });
    oneTimeDatabaseCredentials = result.credentials;
    const initialDatabase = result.credentials.database || "app";
    $("#database-credential-values").textContent = `Host: ${result.database.endpoint}\nPort: ${result.database.port}\nDatabase: ${initialDatabase}\nUsername: ${result.credentials.username}\nPassword: ${result.credentials.password}`;
    $("#database-credentials").hidden = false;
    event.currentTarget.reset();
    $("#database-storage").value = "10Gi";
    await loadDatabases();
  } catch (cause) {
    error.textContent = cause.message;
    error.hidden = false;
  } finally {
    submit.disabled = false;
    submit.textContent = "Create database cluster";
  }
});

$("#copy-database-credentials").addEventListener("click", async () => {
  if (!oneTimeDatabaseCredentials) return;
  try {
    await navigator.clipboard.writeText($("#database-credential-values").textContent);
    $("#copy-database-credentials").textContent = "Copied";
  } catch {
    $("#copy-database-credentials").textContent = "Select and copy the credentials above";
  }
});

const routeMap = {
  applications: { pageId: "#applications-page", title: "Applications" },
  deploy: { pageId: "#deploy-page", title: "Deploy an app" },
  databases: { pageId: "#databases-page", title: "Databases" },
  services: { pageId: "#services-page", title: "Services & storage" },
  storage: { pageId: "#workspace-storage-page", title: "Retained volumes" },
  tokens: { pageId: "#tokens-page", title: "API tokens" },
  team: { pageId: "#team-page", title: "Team" },
  usage: { pageId: "#usage-page", title: "Usage & audit" },
};

function getActiveRoute() {
  const hash = (window.location.hash || "").replace(/^#/, "").toLowerCase();
  return routeMap[hash] ? hash : "applications";
}

function renderRoute(route) {
  const activeRoute = route || getActiveRoute();

  // Hide/show each page
  Object.keys(routeMap).forEach((key) => {
    const el = $(routeMap[key].pageId);
    if (el) el.hidden = (key !== activeRoute);
  });

  // Update topbar breadcrumb
  const pageTitle = $("#page-title");
  if (pageTitle && routeMap[activeRoute]) {
    pageTitle.textContent = routeMap[activeRoute].title;
  }

  // Update sidebar active link
  document.querySelectorAll(".nav-link[href^='#']").forEach((link) => {
    const linkHash = (link.hash || "").replace(/^#/, "").toLowerCase();
    link.classList.toggle("active", linkHash === activeRoute);
  });

  // App runtime panel: only visible in applications when an app is active
  const appRuntime = $("#app-runtime");
  if (appRuntime && activeRoute !== "applications") {
    appRuntime.hidden = true;
  }

  // Trigger page-specific data fetching
  if (activeRoute === "deploy") {
    $("#app-name")?.focus();
    updateSourceFields();
    updateVolumeFields();
    loadClusterContexts();
    checkGitHubInstallation();
  } else if (activeRoute === "tokens") {
    loadTokens();
  } else if (activeRoute === "databases") {
    Promise.all([loadDatabases(), loadDatabaseEngines()]);
  } else if (activeRoute === "services") {
    Promise.all([loadServiceCatalog(), loadManagedServices(), loadConnections(), loadManagedServiceConnectionSources()]);
  } else if (activeRoute === "storage") {
    loadWorkspaceStorage();
  } else if (activeRoute === "team") {
    loadWorkspaceMembers();
  } else if (activeRoute === "usage") {
    loadWorkspaceUsage();
  } else if (activeRoute === "applications") {
    loadDeployments();
    checkGitHubInstallation();
  }
}

window.addEventListener("hashchange", () => renderRoute());

document.querySelectorAll(".nav-link[href^='#']").forEach((link) => {
  link.addEventListener("click", () => {
    const target = (link.hash || "").replace(/^#/, "");
    if (routeMap[target]) {
      window.location.hash = target;
    }
  });
});

document.querySelectorAll(".template-nav-link").forEach((link) => link.addEventListener("click", (event) => {
  event.preventDefault();
  window.location.hash = link.hash.replace(/^#/, "");
}));

$("#show-deploy-form")?.addEventListener("click", () => {
  window.location.hash = "deploy";
});
function updateSourceFields() {
  const imageSource = $("#app-source").value === "image";
  $("#repository-source-fields").hidden = imageSource;
  $("#image-source-fields").hidden = !imageSource;
  $("#app-repository").required = !imageSource;
  $("#app-image").required = imageSource;
  $("#app-port").required = !imageSource;
  $("#image-port").required = imageSource;
  if (imageSource) loadRegistryCredentials();
}
$("#app-source").addEventListener("change", updateSourceFields);
updateSourceFields();
function updateVolumeFields() {
  $("#app-volume-fields").hidden = !$("#app-volume-enabled").checked;
}
$("#app-volume-enabled").addEventListener("change", updateVolumeFields);
updateVolumeFields();
$("#close-runtime").addEventListener("click", closeApplication);
$("#refresh-runtime").addEventListener("click", loadApplicationResources);
$("#refresh-app-events").addEventListener("click", loadApplicationEvents);
$("#refresh-workspace-storage").addEventListener("click", loadWorkspaceStorage);
$("#refresh-logs").addEventListener("click", loadApplicationLogs);
$("#refresh-deployments").addEventListener("click", loadDeployments);
$("#refresh-databases").addEventListener("click", loadDatabases);
$("#refresh-services").addEventListener("click", () => Promise.all([loadServiceCatalog(), loadManagedServices(), loadConnections(), loadManagedServiceConnectionSources()]));

async function loadCustomDomains() {
  if (!activeApplication) return;
  const listEl = $("#app-domains-list");
  if (!listEl) return;
  try {
    const domains = await api(`/api/v1/deployments/${encodeURIComponent(activeApplication.id)}/domains`);
    listEl.replaceChildren();
    if (!domains || domains.length === 0) {
      listEl.innerHTML = '<div class="token-empty">No custom domains configured.</div>';
      return;
    }
    for (const d of domains) {
      const row = document.createElement("div");
      row.className = "token-row";
      row.style.alignItems = "center";

      const details = document.createElement("div");
      details.className = "token-row-details";
      const hostEl = document.createElement("strong");
      hostEl.textContent = d.host;
      const statusBadge = document.createElement("span");
      statusBadge.className = `token-row-state ${d.status === "active" ? "active" : "pending"}`;
      statusBadge.textContent = d.status === "active" ? "Active (TLS Ready)" : "Pending DNS Verification";
      statusBadge.style.marginLeft = "8px";
      hostEl.appendChild(statusBadge);
      details.appendChild(hostEl);

      if (d.status === "pending" && d.txt_name && d.txt_value) {
        const hint = document.createElement("p");
        hint.className = "field-help";
        hint.style.margin = "4px 0 0";
        hint.innerHTML = `Add DNS TXT record: <code>${d.txt_name}</code> with value <code>${d.txt_value}</code>`;
        details.appendChild(hint);
      }

      const actions = document.createElement("div");
      actions.className = "token-row-actions";
      actions.style.display = "flex";
      actions.style.gap = "8px";

      if (d.status === "pending") {
        const verifyBtn = document.createElement("button");
        verifyBtn.className = "inline-action";
        verifyBtn.textContent = "Verify DNS";
        verifyBtn.type = "button";
        verifyBtn.addEventListener("click", async () => {
          verifyBtn.disabled = true;
          verifyBtn.textContent = "Verifying…";
          try {
            await api(`/api/v1/deployments/${encodeURIComponent(activeApplication.id)}/domains/${encodeURIComponent(d.id)}/verify`, { method: "POST" });
            await loadCustomDomains();
          } catch (err) {
            alert(`Domain verification failed: ${err.message}`);
          } finally {
            verifyBtn.disabled = false;
            verifyBtn.textContent = "Verify DNS";
          }
        });
        actions.appendChild(verifyBtn);
      }

      const delBtn = document.createElement("button");
      delBtn.className = "inline-action revoke";
      delBtn.textContent = "Remove";
      delBtn.type = "button";
      delBtn.addEventListener("click", async () => {
        if (!confirm(`Remove custom domain ${d.host}?`)) return;
        delBtn.disabled = true;
        try {
          await api(`/api/v1/deployments/${encodeURIComponent(activeApplication.id)}/domains/${encodeURIComponent(d.id)}`, { method: "DELETE" });
          await loadCustomDomains();
        } catch (err) {
          alert(`Could not remove domain: ${err.message}`);
        } finally {
          delBtn.disabled = false;
        }
      });
      actions.appendChild(delBtn);

      row.appendChild(details);
      row.appendChild(actions);
      listEl.appendChild(row);
    }
  } catch (err) {
    listEl.innerHTML = `<div class="token-empty" style="color: #c45100;">Could not load custom domains: ${err.message}</div>`;
  }
}

$("#app-domain-form")?.addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!activeApplication) return;
  const error = $("#domain-error");
  const submitBtn = $("#add-domain-submit");
  const input = $("#new-custom-domain");
  error.hidden = true;
  submitBtn.disabled = true;
  try {
    await api(`/api/v1/deployments/${encodeURIComponent(activeApplication.id)}/domains`, {
      method: "POST",
      body: JSON.stringify({ host: input.value.trim() }),
    });
    input.value = "";
    await loadCustomDomains();
  } catch (err) {
    error.textContent = err.message;
    error.hidden = false;
  } finally {
    submitBtn.disabled = false;
  }
});
$("#refresh-domains")?.addEventListener("click", loadCustomDomains);

$("#app-env-form")?.addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!activeApplication) return;
  const error = $("#app-env-error");
  const status = $("#app-env-status");
  const submitBtn = $("#app-env-submit");
  error.hidden = true;
  status.textContent = "";

  const parseLines = (text) => {
    const map = {};
    for (const rawLine of (text || "").split("\n")) {
      const line = rawLine.trim();
      if (!line || line.startsWith("#")) continue;
      const idx = line.indexOf("=");
      if (idx > 0) {
        const k = line.slice(0, idx).trim();
        const v = line.slice(idx + 1).trim();
        if (k) map[k] = v;
      }
    }
    return map;
  };

  const envMap = parseLines($("#app-env-vars").value);
  const secretMap = parseLines($("#app-secret-vars").value);

  if (Object.keys(envMap).length === 0 && Object.keys(secretMap).length === 0) {
    error.textContent = "Enter at least one KEY=VALUE environment variable or secret.";
    error.hidden = false;
    return;
  }

  submitBtn.disabled = true;
  status.textContent = "Applying environment variables and secrets to application…";
  try {
    await api(`/api/v1/deployments/${encodeURIComponent(activeApplication.id)}/configuration`, {
      method: "PUT",
      body: JSON.stringify({ environment: envMap, secrets: secretMap }),
    });
    $("#app-secret-vars").value = "";
    status.textContent = "Configuration successfully applied. Secrets are stored securely in Kubernetes.";
    await loadApplicationResources();
  } catch (err) {
    error.textContent = err.message;
    error.hidden = false;
    status.textContent = "Configuration update failed.";
  } finally {
    submitBtn.disabled = false;
  }
});

$("#rollback-app")?.addEventListener("click", async () => {
  if (!activeApplication) return;
  const btn = $("#rollback-app");
  btn.disabled = true;
  try {
    const history = await api(`/api/v1/deployments/${encodeURIComponent(activeApplication.id)}/history`);
    const successfulRuns = (history || []).filter((r) => r.status === "running" && r.id !== activeApplication.latest_run_id);
    if (!successfulRuns.length) {
      alert("No previous successful deployment run found to roll back to.");
      return;
    }
    const targetRun = successfulRuns[0];
    const runDate = new Date(targetRun.created_at).toLocaleString();
    if (!confirm(`Roll back ${activeApplication.name} to run #${targetRun.id} (deployed ${runDate})?`)) {
      return;
    }
    await api(`/api/v1/deployments/${encodeURIComponent(activeApplication.id)}/rollback`, {
      method: "POST",
      body: JSON.stringify({ run_id: targetRun.id }),
    });
    alert(`Rollback queued successfully for ${activeApplication.name}.`);
    await Promise.all([loadDeployments(), loadApplicationResources(), loadApplicationLogs()]);
  } catch (err) {
    alert(`Rollback failed: ${err.message}`);
  } finally {
    btn.disabled = false;
  }
});
$("#signout-button").addEventListener("click", async () => {
  await api("/auth/logout", { method: "POST" });
  window.location.assign("/login");
});

loadAccount().catch((cause) => {
  if (cause.message !== "Sign in required") window.location.assign("/login");
});
loadClusterContexts();
checkGitHubInstallation();
renderRoute();


async function loadWorkspaceMembers() {
  const container = $("#workspace-members");
  try {
    const { members } = await api("/api/v1/workspace/members");
    container.replaceChildren();
    members.forEach((member) => {
      const row = document.createElement("div");
      row.className = "token-row";
      const details = document.createElement("div");
      details.className = "token-row-details";
      const name = document.createElement("strong");
      name.textContent = member.name;
      const email = document.createElement("small");
      email.textContent = member.email;
      details.append(name, email);
      const role = document.createElement("span");
      role.className = "token-row-state";
      role.textContent = member.role;
      row.append(details, role);
      if (currentWorkspaceRole === "owner" && member.role !== "owner") {
        const roleSelect = document.createElement("select");
        roleSelect.className = "text-input";
        ["developer", "viewer"].forEach((value) => {
          const option = document.createElement("option");
          option.value = value;
          option.textContent = value;
          option.selected = value === member.role;
          roleSelect.append(option);
        });
        roleSelect.addEventListener("change", async () => {
          await api(`/api/v1/workspace/members/${member.user_id}`, { method: "PUT", body: JSON.stringify({ role: roleSelect.value }) });
          await loadWorkspaceMembers();
        });
        const remove = document.createElement("button");
        remove.className = "inline-action";
        remove.type = "button";
        remove.textContent = "Remove";
        remove.addEventListener("click", async () => {
          await api(`/api/v1/workspace/members/${member.user_id}`, { method: "DELETE" });
          await loadWorkspaceMembers();
        });
        row.append(roleSelect, remove);
      }
      container.append(row);
    });
    if (!members.length) container.textContent = "No members found.";
  } catch (cause) {
    container.textContent = cause.message;
  }
}

$("#invite-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const error = $("#invite-error");
  error.hidden = true;
  try {
    const result = await api("/api/v1/workspace/invites", { method: "POST", body: JSON.stringify({ email: $("#invite-email").value.trim(), role: $("#invite-role").value }) });
    $("#invite-token").textContent = result.token;
    $("#invite-result").hidden = false;
    event.currentTarget.reset();
  } catch (cause) {
    error.textContent = cause.message;
    error.hidden = false;
  }
});

$("#accept-invite-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const error = $("#accept-invite-error");
  error.hidden = true;
  try {
    await api("/api/v1/workspace/invites/accept", { method: "POST", body: JSON.stringify({ token: $("#accept-invite-token").value.trim() }) });
    window.location.reload();
  } catch (cause) {
    error.textContent = cause.message;
    error.hidden = false;
  }
});

$("#refresh-team").addEventListener("click", loadWorkspaceMembers);

async function loadWorkspaceUsage() {
  const usageContainer = $("#workspace-usage");
  const auditContainer = $("#workspace-audit");
  usageContainer.textContent = "Loading usage…";
  try {
    const [usage, events] = await Promise.all([api("/api/v1/usage"), api("/api/v1/audit")]);
    usageContainer.replaceChildren();
    const card = document.createElement("div");
    card.className = "token-row";
    const detail = document.createElement("div");
    detail.className = "token-row-details";
    const title = document.createElement("strong");
    title.textContent = `${usage.apps} of ${usage.limits.max_apps} apps`;
    const subtitle = document.createElement("small");
    const databaseSummary = usage.database_usage_available
      ? `${usage.databases} databases · ${usage.database_storage_gib}GiB requested storage`
      : "database usage unavailable";
    subtitle.textContent = `${usage.completed_deployments} completed deployments · ${usage.deployment_seconds} build seconds · ${usage.allocated_cpu_millicores}m allocated CPU · ${usage.allocated_memory_mb}Mi allocated memory · ${databaseSummary}`;
    detail.append(title, subtitle);
    card.append(detail);
    usageContainer.append(card);
    renderTable(auditContainer, [
      { label: "Time", value: (item) => new Date(item.created_at).toLocaleString() },
      { label: "Action", value: (item) => item.action },
      { label: "Details", value: (item) => item.details },
    ], events, "No workspace activity recorded yet.");
  } catch (cause) {
    usageContainer.textContent = cause.message;
  }
}

$("#refresh-usage").addEventListener("click", loadWorkspaceUsage);

// --- Workspace API Tokens ---
async function loadTokens() {
  const list = $("#tokens-list");
  if (!list) return;
  try {
    const tokens = await api("/api/v1/tokens");
    list.replaceChildren();
    if (!tokens || !tokens.length) {
      list.innerHTML = '<div class="token-empty"><strong>No API tokens issued</strong><span>Create a token to authenticate the Crelan CLI, GitHub Actions, or CI/CD pipelines.</span></div>';
      return;
    }
    tokens.forEach((token) => {
      const row = document.createElement("div");
      row.className = "token-row";

      const details = document.createElement("div");
      details.className = "token-row-details";
      const name = document.createElement("strong");
      name.textContent = token.name;
      const meta = document.createElement("small");
      const createdStr = token.created_at ? new Date(token.created_at).toLocaleDateString() : "";
      const expStr = token.expires_at ? ("Expires " + new Date(token.expires_at).toLocaleDateString()) : "Never expires";
      meta.textContent = `${createdStr ? "Created " + createdStr + " · " : ""}${expStr}`;
      meta.style.color = "#8491a2";
      details.append(name, meta);

      const keyCol = document.createElement("div");
      const prefix = document.createElement("code");
      prefix.textContent = token.prefix + "••••••••";
      keyCol.append(prefix);

      const scopesCol = document.createElement("div");
      const scopes = document.createElement("span");
      scopes.className = "token-scope-list";
      scopes.textContent = (token.scopes || []).join(", ") || "full-access";
      scopesCol.append(scopes);

      const status = document.createElement("span");
      const isRevoked = !!token.revoked_at;
      const isExpired = token.expires_at && new Date(token.expires_at) < new Date();
      status.className = `token-row-state ${!isRevoked && !isExpired ? "active" : "revoked"}`;
      status.textContent = isRevoked ? "Revoked" : (isExpired ? "Expired" : "Active");

      const action = document.createElement("div");
      if (!isRevoked && !isExpired) {
        const revokeBtn = document.createElement("button");
        revokeBtn.className = "revoke-token-button";
        revokeBtn.type = "button";
        revokeBtn.textContent = "Revoke";
        revokeBtn.addEventListener("click", async () => {
          if (!confirm(`Are you sure you want to revoke "${token.name}"? Pipelines using this token will lose access.`)) return;
          try {
            await api(`/api/v1/tokens/${token.id}`, { method: "DELETE" });
            await loadTokens();
          } catch (err) {
            alert(err.message);
          }
        });
        action.append(revokeBtn);
      }
      row.append(details, keyCol, scopesCol, status, action);
      list.append(row);
    });
  } catch (err) {
    list.innerHTML = `<div class="token-empty"><strong>Failed to load tokens</strong><span>${err.message}</span></div>`;
  }
}

$("#token-create-form")?.addEventListener("submit", async (event) => {
  event.preventDefault();
  const error = $("#token-error");
  const submit = $("#token-submit");
  error.hidden = true;

  let tokenName = $("#token-name")?.value.trim();
  if (!tokenName) {
    tokenName = "ci-deploy-token";
    if ($("#token-name")) $("#token-name").value = tokenName;
  }

  const scopes = Array.from(document.querySelectorAll("input[name='token_scope']:checked")).map((cb) => cb.value);
  if (!scopes.length) {
    error.textContent = "Select at least one access scope.";
    error.hidden = false;
    error.scrollIntoView({ behavior: "smooth", block: "nearest" });
    return;
  }

  submit.disabled = true;
  submit.textContent = "Generating token…";

  const expiryValue = $("#token-expiry")?.value;
  const payload = {
    name: tokenName,
    scopes: scopes,
    expires_in_days: expiryValue ? Number(expiryValue) : undefined,
  };

  try {
    const created = await api("/api/v1/tokens", { method: "POST", body: JSON.stringify(payload) });
    const card = $("#token-created-card");
    if (card) {
      card.hidden = false;
      card.scrollIntoView({ behavior: "smooth", block: "nearest" });
    }
    const val = $("#token-secret-value");
    if (val) val.textContent = created.token;
    await loadTokens();
  } catch (err) {
    error.textContent = err.message;
    error.hidden = false;
    error.scrollIntoView({ behavior: "smooth", block: "nearest" });
  } finally {
    submit.disabled = false;
    submit.textContent = "Generate API token";
  }
});

$("#copy-token-secret")?.addEventListener("click", async () => {
  const val = $("#token-secret-value")?.textContent || "";
  const btn = $("#copy-token-secret");
  try {
    await navigator.clipboard.writeText(val);
    btn.textContent = "✓ Copied to clipboard!";
  } catch {
    const textarea = document.createElement("textarea");
    textarea.value = val;
    document.body.appendChild(textarea);
    textarea.select();
    document.execCommand("copy");
    document.body.removeChild(textarea);
    btn.textContent = "✓ Copied!";
  }
  setTimeout(() => { if (btn) btn.textContent = "Copy token to clipboard"; }, 2500);
});

$("#refresh-tokens")?.addEventListener("click", loadTokens);

// --- Dockerfile Generator ---
const dockerfileTemplates = {
  nodejs: `# Multi-stage Node.js Production Dockerfile
FROM node:20-alpine AS deps
WORKDIR /app
COPY package*.json ./
RUN npm ci --only=production

FROM node:20-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
RUN addgroup --system --gid 1001 nodejs && adduser --system --uid 1001 nodejs
COPY --from=deps /app/node_modules ./node_modules
COPY --chown=nodejs:nodejs . .
USER nodejs
EXPOSE 8080
ENV PORT=8080
CMD ["npm", "start"]
`,
  nextjs: `# Production Dockerfile for Next.js (Standalone output)
FROM node:20-alpine AS deps
WORKDIR /app
COPY package*.json ./
RUN npm ci

FROM node:20-alpine AS builder
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

FROM node:20-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
RUN addgroup --system --gid 1001 nodejs && adduser --system --uid 1001 nextjs
COPY --from=builder /app/public ./public
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static
USER nextjs
EXPOSE 8080
ENV PORT=8080 HOSTNAME="0.0.0.0"
CMD ["node", "server.js"]
`,
  python: `# Production Dockerfile for Python (FastAPI / Flask / Django)
FROM python:3.11-slim AS builder
WORKDIR /app
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

FROM python:3.11-slim AS runner
WORKDIR /app
RUN addgroup --system appgroup && adduser --system --ingroup appgroup appuser
COPY --from=builder /usr/local/lib/python3.11/site-packages /usr/local/lib/python3.11/site-packages
COPY --from=builder /usr/local/bin /usr/local/bin
COPY --chown=appuser:appgroup . .
USER appuser
EXPOSE 8080
ENV PORT=8080
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8080"]
`,
  golang: `# Minimal Multi-Stage Go Production Dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/app .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata \\
    && addgroup -S -g 10001 appgroup \\
    && adduser -S -D -H -u 10001 -G appgroup appuser
WORKDIR /app
COPY --from=builder --chown=10001:10001 /out/app /app/server
USER 10001:10001
EXPOSE 8080
ENV PORT=8080
ENTRYPOINT ["/app/server"]
`,
  rust: `# Multi-stage Rust Production Dockerfile
FROM rust:1.77-slim AS builder
WORKDIR /usr/src/app
COPY . .
RUN cargo build --release

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
RUN useradd -m -u 10001 appuser
WORKDIR /app
COPY --from=builder /usr/src/app/target/release/* /app/server
USER appuser
EXPOSE 8080
ENV PORT=8080
CMD ["/app/server"]
`,
  java: `# Multi-Stage Java / Spring Boot Dockerfile
FROM maven:3.9-eclipse-temurin-21-alpine AS builder
WORKDIR /app
COPY pom.xml .
RUN mvn dependency:go-offline
COPY src ./src
RUN mvn clean package -DskipTests

FROM eclipse-temurin:21-jre-alpine
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
WORKDIR /app
COPY --from=builder /app/target/*.jar app.jar
USER appuser
EXPOSE 8080
ENV PORT=8080
ENTRYPOINT ["java", "-jar", "app.jar"]
`,
  static: `# Static Web Frontend Dockerfile with Nginx
FROM nginx:alpine
COPY . /usr/share/nginx/html
RUN echo 'server { listen 8080; location / { root /usr/share/nginx/html; index index.html; try_files $uri $uri/ /index.html; } }' > /etc/nginx/conf.d/default.conf
EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]
`
};

function updateGeneratedDockerfile() {
  const preset = $("#gen-preset")?.value || "nodejs";
  const code = $("#gen-dockerfile-code");
  if (code) code.textContent = dockerfileTemplates[preset] || "";
}

function openDockerfileModal() {
  const modal = $("#dockerfile-gen-modal");
  if (modal) {
    modal.hidden = false;
    updateGeneratedDockerfile();
  }
}

function closeDockerfileModal() {
  const modal = $("#dockerfile-gen-modal");
  if (modal) modal.hidden = true;
}

function copyGeneratedDockerfile() {
  const content = $("#gen-dockerfile-code")?.textContent || "";
  navigator.clipboard.writeText(content);
  const btn = $("#copy-generated-dockerfile");
  if (btn) {
    btn.textContent = "Copied to clipboard!";
    setTimeout(() => { btn.textContent = "Copy to clipboard"; }, 2000);
  }
}

function applyGeneratedDockerfile() {
  const strat = $("#app-build-strategy");
  if (strat) strat.value = "dockerfile";
  const df = $("#app-dockerfile");
  if (df) df.value = "Dockerfile";
  closeDockerfileModal();
  alert("Dockerfile preset applied! Remember to save this Dockerfile in your repository root as 'Dockerfile'.");
}

window.openDockerfileModal = openDockerfileModal;
window.closeDockerfileModal = closeDockerfileModal;
window.updateGeneratedDockerfile = updateGeneratedDockerfile;
window.copyGeneratedDockerfile = copyGeneratedDockerfile;
window.applyGeneratedDockerfile = applyGeneratedDockerfile;

$("#open-dockerfile-generator")?.addEventListener("click", openDockerfileModal);
$("#close-dockerfile-gen")?.addEventListener("click", closeDockerfileModal);
$("#gen-preset")?.addEventListener("change", updateGeneratedDockerfile);
$("#copy-generated-dockerfile")?.addEventListener("click", copyGeneratedDockerfile);
$("#apply-generated-dockerfile")?.addEventListener("click", applyGeneratedDockerfile);

