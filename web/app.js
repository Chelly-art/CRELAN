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
  if (user.platform_admin === true) $("#admin-nav-link").hidden = false;
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
      const source = document.createElement("code");
      source.textContent = item.image ? `Image: ${item.image}` : (item.repository_url || "Container application");
      details.append(name, source);
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
  if (!window.confirm(`Delete deployment “${item.name}”?`)) return;
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
      error.textContent = "Enter a public GitHub repository URL.";
      error.hidden = false;
      submit.disabled = false;
      submit.textContent = "Deploy application";
      return;
    }
  }
  try {
    await api("/api/v1/deployments", { method: "POST", body: JSON.stringify(payload) });
    form.reset();
    $("#app-port").value = "8080";
    $("#image-port").value = "8080";
    updateSourceFields();
    $("#deploy-form-panel").hidden = true;
    $("#page-title").textContent = "Applications";
    await loadDeployments();
  } catch (cause) {
    error.textContent = cause.message;
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
  await Promise.all([loadApplicationResources(), loadApplicationLogs()]);
}

function closeApplication() {
  activeApplication = null;
  $("#app-runtime").hidden = true;
}

async function loadApplicationResources() {
  if (!activeApplication) return;
  const error = $("#runtime-error");
  error.hidden = true;
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
    error.textContent = cause.message;
    error.hidden = false;
  }
}

async function loadApplicationLogs() {
  if (!activeApplication) return;
  const output = $("#app-logs");
  output.textContent = "Loading recent logs…";
  try {
    const result = await api(`/api/v1/deployments/${activeApplication.id}/logs?tail=200`);
    output.textContent = result.logs || "No log output is available.";
  } catch (cause) {
    output.textContent = cause.message;
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
      row.append(details, status, remove);
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

async function loadDatabaseEngines() {
  try {
    const result = await api("/api/v1/database-engines");
    const select = $("#database-engine");
    for (const engine of result.engines || []) {
      const option = [...select.options].find((item) => item.value === engine.id);
      if (option) {
        option.disabled = !engine.available;
        if (!engine.available) option.textContent = `${engine.name} (operator not installed)`;
      }
    }
  } catch {
    // Keep the catalog visible; create requests return the specific provisioning issue.
  }
}

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

document.querySelectorAll(".nav-link[href^='#']").forEach((link) => link.addEventListener("click", (event) => {
  event.preventDefault();
  const isDeploy = link.hash === "#deploy";
  const isDatabases = link.hash === "#databases";
  const isTeam = link.hash === "#team";
  const isUsage = link.hash === "#usage";
  $("#page-title").textContent = isDeploy ? "Deploy an app" : (isDatabases ? "Databases" : (isTeam ? "Team" : (isUsage ? "Usage & audit" : "Applications")));
  $("#deploy-form-panel").hidden = !isDeploy;
  $("#databases-page").hidden = !isDatabases;
  $("#team-page").hidden = !isTeam;
  $("#usage-page").hidden = !isUsage;
  $(".token-layout").hidden = isDatabases || isTeam || isUsage;
  $("#app-runtime").hidden = isDatabases || isTeam || isUsage || !activeApplication;
  document.querySelectorAll(".nav-link[href^='#']").forEach((item) => item.classList.toggle("active", item === link));
  if (isDeploy) $("#app-name").focus();
  if (isDatabases) Promise.all([loadDatabases(), loadDatabaseEngines()]);
  if (isTeam) loadWorkspaceMembers();
  if (isUsage) loadWorkspaceUsage();
}));
$("#show-deploy-form").addEventListener("click", () => {
  $("#databases-page").hidden = true;
  $(".token-layout").hidden = false;
  $("#deploy-form-panel").hidden = false;
  $("#page-title").textContent = "Deploy an app";
  $("#app-name").focus();
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
$("#close-runtime").addEventListener("click", closeApplication);
$("#refresh-runtime").addEventListener("click", loadApplicationResources);
$("#refresh-logs").addEventListener("click", loadApplicationLogs);
$("#refresh-deployments").addEventListener("click", loadDeployments);
$("#refresh-databases").addEventListener("click", loadDatabases);
$("#signout-button").addEventListener("click", async () => {
  await api("/auth/logout", { method: "POST" });
  window.location.assign("/login");
});

loadAccount().catch((cause) => {
  if (cause.message !== "Sign in required") window.location.assign("/login");
});
loadDeployments();

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
