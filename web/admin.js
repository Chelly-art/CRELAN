// ==========================================================================
// Crelan Lens — Platform Kubernetes Administration Engine
// ==========================================================================

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => document.querySelectorAll(selector);

// Application State
const state = {
  activeView: "overview",
  selectedNamespace: "",
  searchQuery: "",
  perspective: "k8s", // 'k8s' or 'workspace'
  autoRefreshTimer: null,
  cluster: null,
  namespaces: [],
  workspaces: [],
  cachedData: {},
  activeInspector: null, // { kind, namespace, name, raw }
  activeTerminal: null,  // { pod, namespace, container }
  activeLogs: null,      // { pod, namespace, container, timer }
};

// -------------------------------------------------------------
// Core Request Helper
// -------------------------------------------------------------

async function request(path, options = {}) {
  const opts = {
    credentials: "same-origin",
    ...options,
  };
  const response = await fetch(path, opts);
  if (response.status === 401) {
    window.location.assign("/login");
    throw new Error("Sign in required");
  }
  const isJSON = (response.headers.get("content-type") || "").includes("application/json");
  const body = isJSON ? await response.json().catch(() => ({})) : await response.text();
  if (!response.ok) {
    const errMsg = isJSON ? (body.error || `Request failed (${response.status})`) : body;
    const err = new Error(errMsg);
    err.status = response.status;
    throw err;
  }
  return body;
}

function escapeHTML(str) {
  if (str === null || str === undefined) return "";
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#039;");
}

function filterRows(items, fields) {
  if (!state.searchQuery) return items;
  const q = state.searchQuery.toLowerCase();
  return items.filter((item) => {
    return fields.some((f) => {
      const val = item[f];
      if (val === null || val === undefined) return false;
      return String(val).toLowerCase().includes(q);
    });
  });
}

// -------------------------------------------------------------
// Navigation & Routing
// -------------------------------------------------------------

function getRoute() {
  const hash = window.location.hash.replace(/^#/, "");
  return hash || "overview";
}

function setRoute(route) {
  window.location.hash = route;
}

function updateNavigationActive() {
  const current = getRoute();
  state.activeView = current;
  $$(".lens-tree-item").forEach((item) => {
    if (item.dataset.view === current) {
      item.classList.add("active");
    } else {
      item.classList.remove("active");
    }
  });
}

// -------------------------------------------------------------
// Cluster Status & Namespace Select
// -------------------------------------------------------------

async function loadClusterHeader() {
  try {
    const info = await request("/api/v1/admin/cluster/info");
    state.cluster = info;
    $("#sidebar-cluster-name").textContent = info.context || "in-cluster";
    $("#sidebar-cluster-tag").textContent = info.connected ? "Connected" : "Disconnected";
    $("#topbar-context-name").textContent = info.context || "in-cluster";
    $("#topbar-k8s-version").textContent = info.server_version || "v1.34";

    const dot = $("#topbar-dot");
    const railDot = $("#rail-status-dot");
    if (info.connected) {
      dot.style.backgroundColor = "var(--lens-green)";
      railDot.style.backgroundColor = "var(--lens-green)";
    } else {
      dot.style.backgroundColor = "var(--lens-red)";
      railDot.style.backgroundColor = "var(--lens-red)";
    }

    $("#badge-nodes").textContent = info.nodes_count || 0;
    $("#badge-pods").textContent = info.pods_count || 0;
    $("#badge-namespaces").textContent = info.namespaces_count || 0;
    $("#badge-events").textContent = info.warning_events_count || 0;
  } catch (err) {
    console.warn("Could not load cluster header:", err);
  }
}

async function loadNamespacesDropdown() {
  try {
    const list = await request("/api/v1/admin/namespaces");
    state.namespaces = list;
    const select = $("#lens-namespace-select");
    const current = state.selectedNamespace;

    select.replaceChildren();
    const allOpt = document.createElement("option");
    allOpt.value = "";
    allOpt.textContent = "All Namespaces";
    select.append(allOpt);

    list.forEach((ns) => {
      const opt = document.createElement("option");
      opt.value = ns.name;
      opt.textContent = ns.tenant ? `${ns.name} (tenant)` : ns.name;
      if (ns.name === current) opt.selected = true;
      select.append(opt);
    });
  } catch (err) {
    console.warn("Could not load namespaces dropdown:", err);
  }
}

async function loadWorkspaceMappings() {
  try {
    state.workspaces = await request("/api/v1/admin/workspaces/mapping");
  } catch (err) {
    state.workspaces = [];
  }
}

// -------------------------------------------------------------
// View Renderers
// -------------------------------------------------------------

async function renderCurrentView() {
  updateNavigationActive();
  const container = $("#lens-content-area");
  container.innerHTML = `<div class="lens-empty-state">Loading ${state.activeView}…</div>`;

  try {
    switch (state.activeView) {
      case "overview":
        await renderOverview(container);
        break;
      case "nodes":
        await renderNodes(container);
        break;
      case "pods":
        await renderPods(container);
        break;
      case "deployments":
        await renderDeployments(container);
        break;
      case "statefulsets":
        await renderStatefulSets(container);
        break;
      case "daemonsets":
        await renderDaemonSets(container);
        break;
      case "jobs":
        await renderJobs(container);
        break;
      case "cronjobs":
        await renderCronJobs(container);
        break;
      case "services":
        await renderServices(container);
        break;
      case "ingresses":
        await renderIngresses(container);
        break;
      case "networkpolicies":
        await renderNetworkPolicies(container);
        break;
      case "configmaps":
        await renderConfigMaps(container);
        break;
      case "secrets":
        await renderSecrets(container);
        break;
      case "pvcs":
        await renderPVCs(container);
        break;
      case "pvs":
        await renderPVs(container);
        break;
      case "storageclasses":
        await renderStorageClasses(container);
        break;
      case "namespaces":
        await renderNamespaces(container);
        break;
      case "events":
        await renderEvents(container);
        break;
      case "security":
        await renderSecurityAudit(container);
        break;
      case "databases":
        await renderDatabaseTopology(container);
        break;
      case "storage-leaks":
        await renderStorageLeaks(container);
        break;
      case "efficiency":
        await renderEfficiency(container);
        break;
      case "cluster-connect":
        renderClusterConnect(container);
        break;
      case "readiness":
        await renderReadiness(container);
        break;
      case "backup":
        renderBackup(container);
        break;
      default:
        await renderOverview(container);
    }
  } catch (err) {
    container.innerHTML = `
      <div class="lens-empty-state">
        <div class="lens-empty-icon" style="color: var(--lens-red);">⚠️</div>
        <h3>Failed to load view</h3>
        <p>${escapeHTML(err.message)}</p>
        <button class="lens-btn lens-btn-primary" onclick="renderCurrentView()" style="margin-top: 10px;">Retry</button>
      </div>`;
  }
}

// -------------------------------------------------------------
// 1. Cluster Overview
// -------------------------------------------------------------

async function renderOverview(container) {
  const [info, nodes, pods, eff] = await Promise.all([
    request("/api/v1/admin/cluster/info"),
    request("/api/v1/admin/nodes"),
    request("/api/v1/admin/pods"),
    request("/api/v1/admin/resources/efficiency").catch(() => null),
  ]);

  let runningPods = 0;
  let pendingPods = 0;
  let failedPods = 0;
  pods.forEach((p) => {
    if (p.has_errors || p.phase === "Failed" || p.phase === "CrashLoopBackOff" || p.phase === "OOMKilled") {
      failedPods++;
    } else if (p.phase === "Pending") {
      pendingPods++;
    } else {
      runningPods++;
    }
  });

  const totalPods = pods.length || 1;
  const runPct = (runningPods / totalPods) * 100;
  const pendPct = (pendingPods / totalPods) * 100;
  const failPct = (failedPods / totalPods) * 100;

  const html = `
    <div class="lens-view-header">
      <div class="lens-view-title">
        <h1>Cluster Overview</h1>
        <span class="lens-view-count">${escapeHTML(info.context || "in-cluster")}</span>
      </div>
      <div class="lens-view-actions">
        <a class="lens-btn" href="#security">🛡️ Security Audit</a>
        <a class="lens-btn" href="#efficiency">📊 Resource Heatmap</a>
        <a class="lens-btn lens-btn-primary" href="#cluster-connect">Switch / Reconnect</a>
      </div>
    </div>

    <div class="lens-overview-grid">
      <div class="lens-stat-card">
        <div class="lens-stat-header"><span>Cluster Health</span><span>☸ K8s</span></div>
        <div class="lens-stat-value" style="color: ${info.ready_nodes > 0 ? "var(--lens-green)" : "var(--lens-red)"};">
          ${info.connected ? "Healthy" : "Offline"}
        </div>
        <div class="lens-stat-subtext">${escapeHTML(info.server_version || "Kubernetes")} (${escapeHTML(info.platform || "linux/amd64")})</div>
      </div>

      <div class="lens-stat-card">
        <div class="lens-stat-header"><span>Ready Nodes</span><span>Nodes</span></div>
        <div class="lens-stat-value">${info.ready_nodes} / ${info.nodes_count}</div>
        <div class="lens-progress-wrap">
          <div class="lens-progress-bar">
            <div class="lens-progress-fill green" style="width: ${info.nodes_count > 0 ? (info.ready_nodes / info.nodes_count) * 100 : 0}%;"></div>
          </div>
        </div>
      </div>

      <div class="lens-stat-card">
        <div class="lens-stat-header"><span>Workloads (Pods)</span><span>Pods</span></div>
        <div class="lens-stat-value">${pods.length}</div>
        <div class="lens-breakdown-bar">
          <div class="lens-breakdown-segment running" style="width: ${runPct}%;" title="Running: ${runningPods}"></div>
          <div class="lens-breakdown-segment pending" style="width: ${pendPct}%;" title="Pending: ${pendingPods}"></div>
          <div class="lens-breakdown-segment failed" style="width: ${failPct}%;" title="Failed: ${failedPods}"></div>
        </div>
        <div class="lens-stat-subtext">${runningPods} running · ${failedPods} with errors</div>
      </div>

      <div class="lens-stat-card">
        <div class="lens-stat-header"><span>Customer Workspaces</span><span>PaaS</span></div>
        <div class="lens-stat-value">${info.workspaces_count || 0}</div>
        <div class="lens-stat-subtext">${info.deployments_count || 0} active deployments</div>
      </div>
    </div>

    <!-- Cluster Nodes Quick Summary -->
    <div style="margin-bottom: 24px;">
      <div class="lens-view-header">
        <div class="lens-view-title"><h2 style="font-size: 15px; margin: 0;">Cluster Nodes</h2></div>
        <a class="lens-btn lens-btn-sm" href="#nodes">View All Nodes →</a>
      </div>
      <div class="lens-table-container">
        <table class="lens-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Status</th>
              <th>Roles</th>
              <th>Version</th>
              <th>Internal IP</th>
              <th>Pods</th>
              <th>CPU Alloc</th>
              <th>Mem Alloc</th>
            </tr>
          </thead>
          <tbody>
            ${nodes.map((n) => `
              <tr onclick="openInspector('node', '', '${escapeHTML(n.name)}')">
                <td class="lens-table-name">${escapeHTML(n.name)}</td>
                <td><span class="lens-badge ${n.status === "Ready" ? "lens-badge-ready" : "lens-badge-error"}"><span class="lens-badge-dot"></span>${escapeHTML(n.status)}</span></td>
                <td>${escapeHTML(n.roles.join(", "))}</td>
                <td>${escapeHTML(n.version)}</td>
                <td>${escapeHTML(n.internal_ip)}</td>
                <td>${n.pods_running} / ${n.pods_capacity}</td>
                <td>${escapeHTML(n.cpu_allocatable)}</td>
                <td>${escapeHTML(n.mem_allocatable)}</td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      </div>
    </div>

    <!-- Warning Events Preview -->
    <div>
      <div class="lens-view-header">
        <div class="lens-view-title"><h2 style="font-size: 15px; margin: 0;">Recent Cluster Warning Events</h2></div>
        <a class="lens-btn lens-btn-sm" href="#events">View All Events →</a>
      </div>
      <div id="overview-events-container">
        <div class="lens-empty-state" style="padding: 20px;">Loading warning events…</div>
      </div>
    </div>
  `;
  container.innerHTML = html;

  // Load warning events async
  try {
    const events = await request("/api/v1/admin/events?type=Warning");
    const evContainer = $("#overview-events-container");
    if (!events.length) {
      evContainer.innerHTML = `<div class="lens-empty-state" style="padding: 20px;">No warning events in the cluster. Everything is operating normally.</div>`;
    } else {
      evContainer.innerHTML = `
        <div class="lens-table-container">
          <table class="lens-table">
            <thead>
              <tr>
                <th>Reason</th>
                <th>Object</th>
                <th>Namespace</th>
                <th>Message</th>
                <th>Age</th>
              </tr>
            </thead>
            <tbody>
              ${events.slice(0, 10).map((e) => `
                <tr>
                  <td><span class="lens-badge lens-badge-warning"><span class="lens-badge-dot"></span>${escapeHTML(e.reason)}</span></td>
                  <td><strong>${escapeHTML(e.object_kind)}/${escapeHTML(e.object_name)}</strong></td>
                  <td class="lens-table-ns">${escapeHTML(e.namespace || "-")}</td>
                  <td style="max-width: 400px; overflow: hidden; text-overflow: ellipsis;">${escapeHTML(e.message)}</td>
                  <td class="lens-table-age">${escapeHTML(e.age)}</td>
                </tr>
              `).join("")}
            </tbody>
          </table>
        </div>`;
    }
  } catch (e) {
    // ignore
  }
}

// -------------------------------------------------------------
// 2. Nodes View
// -------------------------------------------------------------

async function renderNodes(container) {
  const nodes = await request("/api/v1/admin/nodes");
  const filtered = filterRows(nodes, ["name", "status", "internal_ip", "version"]);

  container.innerHTML = `
    <div class="lens-view-header">
      <div class="lens-view-title">
        <h1>Nodes</h1>
        <span class="lens-view-count">${nodes.length}</span>
      </div>
    </div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Status</th>
            <th>Roles</th>
            <th>Version</th>
            <th>OS / Arch</th>
            <th>Internal IP</th>
            <th>Pods</th>
            <th>CPU</th>
            <th>Memory</th>
            <th>Age</th>
          </tr>
        </thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="10" class="lens-empty-state">No nodes match your filter.</td></tr>` : ""}
          ${filtered.map((n) => `
            <tr onclick="openInspector('node', '', '${escapeHTML(n.name)}')">
              <td class="lens-table-name">${escapeHTML(n.name)}</td>
              <td><span class="lens-badge ${n.status === "Ready" ? "lens-badge-ready" : "lens-badge-error"}"><span class="lens-badge-dot"></span>${escapeHTML(n.status)}</span></td>
              <td>${escapeHTML(n.roles.join(", "))}</td>
              <td class="lens-table-ns">${escapeHTML(n.version)}</td>
              <td style="font-size: 11px; color: var(--lens-text-muted);">${escapeHTML(n.os_image)}</td>
              <td class="lens-table-ns">${escapeHTML(n.internal_ip)}</td>
              <td>${n.pods_running} / ${n.pods_capacity}</td>
              <td>${escapeHTML(n.cpu_allocatable)}</td>
              <td>${escapeHTML(n.mem_allocatable)}</td>
              <td class="lens-table-age">${escapeHTML(n.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>
  `;
}

// -------------------------------------------------------------
// 3. Workloads: Pods View (with AI Diagnose, Logs, Exec, Delete)
// -------------------------------------------------------------

async function renderPods(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const pods = await request(`/api/v1/admin/pods${ns}`);
  const filtered = filterRows(pods, ["name", "namespace", "phase", "node", "pod_ip", "workspace_name", "app_name"]);

  container.innerHTML = `
    <div class="lens-view-header">
      <div class="lens-view-title">
        <h1>Pods</h1>
        <span class="lens-view-count">${pods.length}</span>
      </div>
      <div class="lens-view-actions">
        <button class="lens-btn lens-btn-sm" onclick="renderPods($('#lens-content-area'))">⟳ Refresh</button>
      </div>
    </div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Namespace</th>
            ${state.perspective === "workspace" ? "<th>Workspace / App</th>" : ""}
            <th>Containers</th>
            <th>Restarts</th>
            <th>Controlled By</th>
            <th>Node</th>
            <th>Status</th>
            <th>Age</th>
            <th style="text-align: right;">Actions</th>
          </tr>
        </thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="10" class="lens-empty-state">No pods found in namespace.</td></tr>` : ""}
          ${filtered.map((p) => {
            let badgeClass = "lens-badge-running";
            if (p.phase === "Pending") badgeClass = "lens-badge-pending";
            if (p.has_errors || p.phase === "CrashLoopBackOff" || p.phase === "Failed" || p.phase === "OOMKilled" || p.phase === "Error") badgeClass = "lens-badge-crashloop";
            if (p.phase === "Succeeded" || p.phase === "Completed") badgeClass = "lens-badge-completed";

            return `
              <tr class="${p.has_errors ? "row-error" : ""}">
                <td class="lens-table-name" onclick="openInspector('pod', '${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')">
                  ${escapeHTML(p.name)}
                </td>
                <td class="lens-table-ns">${escapeHTML(p.namespace)}</td>
                ${state.perspective === "workspace" ? `
                  <td>
                    ${p.workspace_name ? `<span class="lens-ws-tag">${escapeHTML(p.workspace_name)}</span>` : `<span style="color: var(--lens-text-dim);">-</span>`}
                    ${p.app_name ? `<small style="display:block; color: var(--lens-text-muted);">${escapeHTML(p.app_name)}</small>` : ""}
                  </td>` : ""}
                <td>${escapeHTML(p.ready_containers)}</td>
                <td style="color: ${p.restarts > 0 ? "var(--lens-amber)" : "inherit"}; font-weight: ${p.restarts > 0 ? "600" : "normal"};">${p.restarts}</td>
                <td class="lens-table-ns">${escapeHTML(p.controlled_by || "-")}</td>
                <td class="lens-table-ns">${escapeHTML(p.node || "-")}</td>
                <td><span class="lens-badge ${badgeClass}"><span class="lens-badge-dot"></span>${escapeHTML(p.phase)}</span></td>
                <td class="lens-table-age">${escapeHTML(p.age)}</td>
                <td>
                  <div class="lens-row-actions" style="justify-content: flex-end;">
                    <button class="lens-action-icon-btn btn-diagnose" onclick="event.stopPropagation(); openAIDiagnosis('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="🤖 1-Click AI Root-Cause Diagnosis">🤖</button>
                    <button class="lens-action-icon-btn" onclick="event.stopPropagation(); openPodLogs('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="View Container Logs">📜</button>
                    <button class="lens-action-icon-btn" onclick="event.stopPropagation(); openPodTerminal('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Interactive Terminal / Exec">🖥️</button>
                    <button class="lens-action-icon-btn btn-delete" onclick="event.stopPropagation(); confirmDeletePod('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Delete / Restart Pod">🗑️</button>
                  </div>
                </td>
              </tr>
            `;
          }).join("")}
        </tbody>
      </table>
    </div>
  `;
}

// -------------------------------------------------------------
// 4. Workloads: Deployments View (with Scale, Rollout Restart)
// -------------------------------------------------------------

async function renderDeployments(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const deps = await request(`/api/v1/admin/deployments${ns}`);
  const filtered = filterRows(deps, ["name", "namespace", "workspace_name", "app_name"]);

  container.innerHTML = `
    <div class="lens-view-header">
      <div class="lens-view-title">
        <h1>Deployments</h1>
        <span class="lens-view-count">${deps.length}</span>
      </div>
    </div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Namespace</th>
            ${state.perspective === "workspace" ? "<th>Workspace</th>" : ""}
            <th>Replicas</th>
            <th>Available</th>
            <th>Images</th>
            <th>Conditions</th>
            <th>Age</th>
            <th style="text-align: right;">Actions</th>
          </tr>
        </thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="9" class="lens-empty-state">No deployments found.</td></tr>` : ""}
          ${filtered.map((d) => `
            <tr>
              <td class="lens-table-name" onclick="openInspector('deployment', '${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')">
                ${escapeHTML(d.name)}
              </td>
              <td class="lens-table-ns">${escapeHTML(d.namespace)}</td>
              ${state.perspective === "workspace" ? `<td>${d.workspace_name ? `<span class="lens-ws-tag">${escapeHTML(d.workspace_name)}</span>` : "-"}</td>` : ""}
              <td><strong>${d.ready_replicas} / ${d.replicas}</strong></td>
              <td>${d.available_replicas}</td>
              <td class="lens-table-ns" style="max-width: 250px; overflow: hidden; text-overflow: ellipsis;">${escapeHTML(d.images.join(", "))}</td>
              <td>${escapeHTML(d.conditions.join(", ") || "Progressing")}</td>
              <td class="lens-table-age">${escapeHTML(d.age)}</td>
              <td>
                <div class="lens-row-actions" style="justify-content: flex-end;">
                  <button class="lens-action-icon-btn" onclick="event.stopPropagation(); triggerRolloutRestart('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')" title="Rollout Restart">🔄</button>
                  <button class="lens-action-icon-btn" onclick="event.stopPropagation(); openScaleModal('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}', ${d.replicas})" title="Scale Replicas">⚖️</button>
                  <button class="lens-action-icon-btn btn-delete" onclick="event.stopPropagation(); confirmDeleteDeployment('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')" title="Delete Deployment">🗑️</button>
                </div>
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>
  `;
}

// -------------------------------------------------------------
// 5. StatefulSets, DaemonSets, Jobs, CronJobs
// -------------------------------------------------------------

async function renderStatefulSets(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const list = await request(`/api/v1/admin/statefulsets${ns}`);
  const filtered = filterRows(list, ["name", "namespace"]);

  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>StatefulSets</h1><span class="lens-view-count">${list.length}</span></div></div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Replicas</th><th>Images</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="5" class="lens-empty-state">No statefulsets found.</td></tr>` : ""}
          ${filtered.map((s) => `
            <tr>
              <td class="lens-table-name">${escapeHTML(s.name)}</td>
              <td class="lens-table-ns">${escapeHTML(s.namespace)}</td>
              <td><strong>${s.ready_replicas} / ${s.replicas}</strong></td>
              <td class="lens-table-ns">${escapeHTML(s.images.join(", "))}</td>
              <td class="lens-table-age">${escapeHTML(s.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderDaemonSets(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const list = await request(`/api/v1/admin/daemonsets${ns}`);
  const filtered = filterRows(list, ["name", "namespace"]);

  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>DaemonSets</h1><span class="lens-view-count">${list.length}</span></div></div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Desired</th><th>Current</th><th>Ready</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="6" class="lens-empty-state">No daemonsets found.</td></tr>` : ""}
          ${filtered.map((d) => `
            <tr>
              <td class="lens-table-name">${escapeHTML(d.name)}</td>
              <td class="lens-table-ns">${escapeHTML(d.namespace)}</td>
              <td>${d.desired}</td>
              <td>${d.current}</td>
              <td>${d.ready}</td>
              <td class="lens-table-age">${escapeHTML(d.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderJobs(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const list = await request(`/api/v1/admin/jobs${ns}`);
  const filtered = filterRows(list, ["name", "namespace"]);

  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>Jobs</h1><span class="lens-view-count">${list.length}</span></div></div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Completions</th><th>Duration</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="5" class="lens-empty-state">No batch jobs found.</td></tr>` : ""}
          ${filtered.map((j) => `
            <tr>
              <td class="lens-table-name">${escapeHTML(j.name)}</td>
              <td class="lens-table-ns">${escapeHTML(j.namespace)}</td>
              <td>${j.succeeded} / ${j.completions}</td>
              <td>${escapeHTML(j.duration || "-")}</td>
              <td class="lens-table-age">${escapeHTML(j.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderCronJobs(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const list = await request(`/api/v1/admin/cronjobs${ns}`);
  const filtered = filterRows(list, ["name", "namespace", "schedule"]);

  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>CronJobs</h1><span class="lens-view-count">${list.length}</span></div></div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Schedule</th><th>Suspend</th><th>Active</th><th>Last Schedule</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="7" class="lens-empty-state">No cronjobs found.</td></tr>` : ""}
          ${filtered.map((c) => `
            <tr>
              <td class="lens-table-name">${escapeHTML(c.name)}</td>
              <td class="lens-table-ns">${escapeHTML(c.namespace)}</td>
              <td><code>${escapeHTML(c.schedule)}</code></td>
              <td>${c.suspend ? "Yes" : "No"}</td>
              <td>${c.active}</td>
              <td>${escapeHTML(c.last_schedule || "-")}</td>
              <td class="lens-table-age">${escapeHTML(c.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

// -------------------------------------------------------------
// 6. Network: Services, Ingresses, NetworkPolicies
// -------------------------------------------------------------

async function renderServices(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const list = await request(`/api/v1/admin/services${ns}`);
  const filtered = filterRows(list, ["name", "namespace", "type", "cluster_ip"]);

  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>Services</h1><span class="lens-view-count">${list.length}</span></div></div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Type</th><th>Cluster IP</th><th>External IP</th><th>Ports</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="7" class="lens-empty-state">No services found.</td></tr>` : ""}
          ${filtered.map((s) => `
            <tr onclick="openInspector('service', '${escapeHTML(s.namespace)}', '${escapeHTML(s.name)}')">
              <td class="lens-table-name">${escapeHTML(s.name)}</td>
              <td class="lens-table-ns">${escapeHTML(s.namespace)}</td>
              <td><span class="lens-badge lens-badge-completed">${escapeHTML(s.type)}</span></td>
              <td class="lens-table-ns">${escapeHTML(s.cluster_ip)}</td>
              <td>${escapeHTML(s.external_ips.join(", ") || "-")}</td>
              <td>${escapeHTML(s.ports.join(", "))}</td>
              <td class="lens-table-age">${escapeHTML(s.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderIngresses(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const list = await request(`/api/v1/admin/ingresses${ns}`);
  const filtered = filterRows(list, ["name", "namespace"]);

  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>Ingresses</h1><span class="lens-view-count">${list.length}</span></div></div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Class</th><th>Hosts</th><th>Addresses</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="6" class="lens-empty-state">No ingresses found.</td></tr>` : ""}
          ${filtered.map((i) => `
            <tr onclick="openInspector('ingress', '${escapeHTML(i.namespace)}', '${escapeHTML(i.name)}')">
              <td class="lens-table-name">${escapeHTML(i.name)}</td>
              <td class="lens-table-ns">${escapeHTML(i.namespace)}</td>
              <td>${escapeHTML(i.ingress_class || "-")}</td>
              <td>${escapeHTML(i.hosts.join(", ") || "*")}</td>
              <td>${escapeHTML(i.addresses.join(", ") || "-")}</td>
              <td class="lens-table-age">${escapeHTML(i.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderNetworkPolicies(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const list = await request(`/api/v1/admin/networkpolicies${ns}`);
  const filtered = filterRows(list, ["name", "namespace"]);

  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>Network Policies</h1><span class="lens-view-count">${list.length}</span></div></div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Pod Selector</th><th>Policy Types</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="5" class="lens-empty-state">No network policies found.</td></tr>` : ""}
          ${filtered.map((np) => `
            <tr>
              <td class="lens-table-name">${escapeHTML(np.name)}</td>
              <td class="lens-table-ns">${escapeHTML(np.namespace)}</td>
              <td><code>${escapeHTML(np.pod_selector || "All pods")}</code></td>
              <td>${escapeHTML(np.policy_types.join(", "))}</td>
              <td class="lens-table-age">${escapeHTML(np.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

// -------------------------------------------------------------
// 7. Config & Storage
// -------------------------------------------------------------

async function renderConfigMaps(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const list = await request(`/api/v1/admin/configmaps${ns}`);
  const filtered = filterRows(list, ["name", "namespace"]);

  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>ConfigMaps</h1><span class="lens-view-count">${list.length}</span></div></div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Keys</th><th>Data Keys</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="5" class="lens-empty-state">No configmaps found.</td></tr>` : ""}
          ${filtered.map((cm) => `
            <tr onclick="openInspector('configmap', '${escapeHTML(cm.namespace)}', '${escapeHTML(cm.name)}')">
              <td class="lens-table-name">${escapeHTML(cm.name)}</td>
              <td class="lens-table-ns">${escapeHTML(cm.namespace)}</td>
              <td>${cm.keys_count}</td>
              <td class="lens-table-ns">${escapeHTML(cm.keys.slice(0, 5).join(", "))}${cm.keys.length > 5 ? "…" : ""}</td>
              <td class="lens-table-age">${escapeHTML(cm.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderSecrets(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const list = await request(`/api/v1/admin/secrets${ns}`);
  const filtered = filterRows(list, ["name", "namespace", "type"]);

  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>Secrets</h1><span class="lens-view-count">${list.length}</span></div></div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Type</th><th>Keys</th><th>Data Keys</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="6" class="lens-empty-state">No secrets found.</td></tr>` : ""}
          ${filtered.map((s) => `
            <tr onclick="openInspector('secret', '${escapeHTML(s.namespace)}', '${escapeHTML(s.name)}')">
              <td class="lens-table-name">🔒 ${escapeHTML(s.name)}</td>
              <td class="lens-table-ns">${escapeHTML(s.namespace)}</td>
              <td><span class="lens-badge lens-badge-completed">${escapeHTML(s.type)}</span></td>
              <td>${s.keys_count}</td>
              <td class="lens-table-ns">${escapeHTML(s.keys.slice(0, 5).join(", "))}${s.keys.length > 5 ? "…" : ""}</td>
              <td class="lens-table-age">${escapeHTML(s.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderPVCs(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const list = await request(`/api/v1/admin/pvcs${ns}`);
  const filtered = filterRows(list, ["name", "namespace", "volume_name", "storage_class"]);

  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>Persistent Volume Claims</h1><span class="lens-view-count">${list.length}</span></div></div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Status</th><th>Capacity</th><th>StorageClass</th><th>Volume</th><th>Age</th><th style="text-align: right;">Action</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="8" class="lens-empty-state">No volume claims found.</td></tr>` : ""}
          ${filtered.map((p) => `
            <tr onclick="openInspector('pvc', '${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')">
              <td class="lens-table-name">💾 ${escapeHTML(p.name)}</td>
              <td class="lens-table-ns">${escapeHTML(p.namespace)}</td>
              <td><span class="lens-badge ${p.status === "Bound" ? "lens-badge-ready" : "lens-badge-warning"}">${escapeHTML(p.status)}</span></td>
              <td>${escapeHTML(p.capacity || "-")}</td>
              <td>${escapeHTML(p.storage_class || "-")}</td>
              <td class="lens-table-ns">${escapeHTML(p.volume_name || "-")}</td>
              <td class="lens-table-age">${escapeHTML(p.age)}</td>
              <td style="text-align: right;">
                <button class="lens-action-icon-btn btn-delete" onclick="event.stopPropagation(); deletePVC('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Delete PVC">🗑️</button>
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderPVs(container) {
  const list = await request("/api/v1/admin/pvs");
  const filtered = filterRows(list, ["name", "storage_class", "status"]);

  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>Persistent Volumes</h1><span class="lens-view-count">${list.length}</span></div></div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Name</th><th>Capacity</th><th>Access Modes</th><th>Reclaim</th><th>Status</th><th>Claim</th><th>StorageClass</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="8" class="lens-empty-state">No persistent volumes found.</td></tr>` : ""}
          ${filtered.map((pv) => `
            <tr>
              <td class="lens-table-name">${escapeHTML(pv.name)}</td>
              <td>${escapeHTML(pv.capacity)}</td>
              <td>${escapeHTML(pv.access_modes.join(", "))}</td>
              <td>${escapeHTML(pv.reclaim_policy)}</td>
              <td><span class="lens-badge ${pv.status === "Bound" ? "lens-badge-ready" : "lens-badge-warning"}">${escapeHTML(pv.status)}</span></td>
              <td class="lens-table-ns">${escapeHTML(pv.claim || "-")}</td>
              <td>${escapeHTML(pv.storage_class || "-")}</td>
              <td class="lens-table-age">${escapeHTML(pv.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderStorageClasses(container) {
  const list = await request("/api/v1/admin/storageclasses");
  const filtered = filterRows(list, ["name", "provisioner"]);

  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>Storage Classes</h1><span class="lens-view-count">${list.length}</span></div></div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Name</th><th>Provisioner</th><th>Reclaim Policy</th><th>Volume Binding</th><th>Expansion</th><th>Default</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="7" class="lens-empty-state">No storage classes found.</td></tr>` : ""}
          ${filtered.map((sc) => `
            <tr>
              <td class="lens-table-name">${escapeHTML(sc.name)}</td>
              <td class="lens-table-ns">${escapeHTML(sc.provisioner)}</td>
              <td>${escapeHTML(sc.reclaim_policy || "-")}</td>
              <td>${escapeHTML(sc.volume_binding_mode || "-")}</td>
              <td>${sc.allow_volume_expansion ? "Yes" : "No"}</td>
              <td>${sc.is_default ? `<span class="lens-badge lens-badge-ready">Default</span>` : "-"}</td>
              <td class="lens-table-age">${escapeHTML(sc.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

// -------------------------------------------------------------
// 8. Namespaces & Events
// -------------------------------------------------------------

async function renderNamespaces(container) {
  const list = await request("/api/v1/admin/namespaces");
  const filtered = filterRows(list, ["name", "tenant", "status"]);

  container.innerHTML = `
    <div class="lens-view-header">
      <div class="lens-view-title"><h1>Namespaces</h1><span class="lens-view-count">${list.length}</span></div>
      <div class="lens-view-actions">
        <button class="lens-btn lens-btn-primary" onclick="openCreateNamespaceModal()">+ Create Namespace</button>
      </div>
    </div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Name</th><th>Status</th><th>Capsule Tenant</th><th>Workspace</th><th>Age</th><th style="text-align: right;">Action</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="6" class="lens-empty-state">No namespaces found.</td></tr>` : ""}
          ${filtered.map((ns) => `
            <tr>
              <td class="lens-table-name" onclick="selectNamespaceAndFilter('${escapeHTML(ns.name)}')">📁 ${escapeHTML(ns.name)}</td>
              <td><span class="lens-badge ${ns.status === "Active" ? "lens-badge-ready" : "lens-badge-warning"}">${escapeHTML(ns.status)}</span></td>
              <td>${ns.tenant ? `<span class="lens-badge lens-badge-completed">Capsule: ${escapeHTML(ns.tenant)}</span>` : `<span style="color: var(--lens-text-dim);">-</span>`}</td>
              <td>${ns.workspace_id ? `<span class="lens-ws-tag">WS #${escapeHTML(ns.workspace_id)}</span>` : "-"}</td>
              <td class="lens-table-age">${escapeHTML(ns.age)}</td>
              <td style="text-align: right;">
                ${ns.name !== "default" && ns.name !== "kube-system" ? `<button class="lens-action-icon-btn btn-delete" onclick="deleteNamespace('${escapeHTML(ns.name)}')" title="Delete Namespace">🗑️</button>` : ""}
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

function selectNamespaceAndFilter(name) {
  state.selectedNamespace = name;
  $("#lens-namespace-select").value = name;
  setRoute("pods");
}

async function renderEvents(container) {
  const ns = state.selectedNamespace ? `&namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const list = await request(`/api/v1/admin/events?type=${ns}`);
  const filtered = filterRows(list, ["reason", "message", "object_name", "object_kind", "namespace"]);

  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>Cluster Events</h1><span class="lens-view-count">${list.length}</span></div></div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Type</th><th>Reason</th><th>Involved Object</th><th>Namespace</th><th>Message</th><th>Count</th><th>Last Seen</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="7" class="lens-empty-state">No events found.</td></tr>` : ""}
          ${filtered.map((e) => `
            <tr>
              <td><span class="lens-badge ${e.type === "Warning" ? "lens-badge-warning" : "lens-badge-completed"}">${escapeHTML(e.type)}</span></td>
              <td><strong>${escapeHTML(e.reason)}</strong></td>
              <td class="lens-table-ns"><strong>${escapeHTML(e.object_kind)}</strong>/${escapeHTML(e.object_name)}</td>
              <td class="lens-table-ns">${escapeHTML(e.namespace || "-")}</td>
              <td style="max-width: 450px; overflow: hidden; text-overflow: ellipsis;">${escapeHTML(e.message)}</td>
              <td>${e.count}</td>
              <td class="lens-table-age">${escapeHTML(e.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

// -------------------------------------------------------------
// 9. Super-Lens Advanced Views (Security, DB, Leaks, Efficiency)
// -------------------------------------------------------------

async function renderSecurityAudit(container) {
  const audit = await request("/api/v1/admin/security/audit");

  let scoreColor = "green";
  if (audit.score < 60) scoreColor = "red";
  else if (audit.score < 85) scoreColor = "amber";

  container.innerHTML = `
    <div class="lens-view-header">
      <div class="lens-view-title"><h1>🛡️ Cluster Security & Isolation Audit</h1></div>
      <button class="lens-btn lens-btn-primary" onclick="renderSecurityAudit($('#lens-content-area'))">Re-scan Cluster</button>
    </div>

    <div class="lens-score-banner">
      <div class="lens-score-circle ${scoreColor}">${audit.score}%</div>
      <div class="lens-score-details">
        <h3>Cluster Security Posture: ${audit.score >= 80 ? "Good" : "Needs Attention"}</h3>
        <p>${audit.total_pods_scanned} pods evaluated · ${audit.passed_checks} checks passed · ${audit.failed_checks} findings flagged</p>
        <p style="margin-top: 6px;">Capsule Multi-Tenant Isolation: <strong style="color: ${audit.tenant_isolation.status === "Secure" ? "var(--lens-green)" : "var(--lens-amber)"};">${escapeHTML(audit.tenant_isolation.status)}</strong> (${audit.tenant_isolation.enforced_namespaces}/${audit.tenant_isolation.total_tenant_namespaces} namespaces enforced with NetworkPolicy)</p>
      </div>
    </div>

    <h2 style="font-size: 15px; margin: 20px 0 10px;">Security Findings</h2>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Severity</th><th>Rule</th><th>Pod</th><th>Namespace</th><th>Description</th><th>Remediation</th></tr></thead>
        <tbody>
          ${audit.issues.length === 0 ? `<tr><td colspan="6" class="lens-empty-state">🎉 Outstanding! Zero security issues detected in active workloads.</td></tr>` : ""}
          ${audit.issues.map((i) => `
            <tr>
              <td><span class="lens-badge ${i.severity === "critical" ? "lens-badge-crashloop" : "lens-badge-warning"}">${escapeHTML(i.severity.toUpperCase())}</span></td>
              <td><strong>${escapeHTML(i.rule)}</strong></td>
              <td class="lens-table-name" onclick="openInspector('pod', '${escapeHTML(i.namespace)}', '${escapeHTML(i.pod_name)}')">${escapeHTML(i.pod_name)}</td>
              <td class="lens-table-ns">${escapeHTML(i.namespace)}</td>
              <td>${escapeHTML(i.description)}</td>
              <td style="color: var(--lens-primary); font-size: 11px;">${escapeHTML(i.remediation)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderDatabaseTopology(container) {
  const topo = await request("/api/v1/admin/databases/topology");

  container.innerHTML = `
    <div class="lens-view-header">
      <div class="lens-view-title"><h1>🗄️ Managed Database & Operator Topology</h1></div>
      <button class="lens-btn lens-btn-primary" onclick="renderDatabaseTopology($('#lens-content-area'))">⟳ Refresh</button>
    </div>

    <div class="lens-overview-grid">
      <div class="lens-stat-card">
        <div class="lens-stat-header"><span>PostgreSQL Clusters</span><span>CloudNativePG</span></div>
        <div class="lens-stat-value">${topo.postgres_clusters ? topo.postgres_clusters.length : 0}</div>
        <div class="lens-stat-subtext">3-node HA with streaming replication</div>
      </div>
      <div class="lens-stat-card">
        <div class="lens-stat-header"><span>MySQL Clusters</span><span>InnoDB</span></div>
        <div class="lens-stat-value">${topo.mysql_clusters ? topo.mysql_clusters.length : 0}</div>
        <div class="lens-stat-subtext">3-instance quorum cluster</div>
      </div>
      <div class="lens-stat-card">
        <div class="lens-stat-header"><span>Storage Leaks</span><span>Orphan PVCs</span></div>
        <div class="lens-stat-value" style="color: ${topo.storage_leaks && topo.storage_leaks.length > 0 ? "var(--lens-amber)" : "var(--lens-green)"};">
          ${topo.storage_leaks ? topo.storage_leaks.length : 0}
        </div>
        <div class="lens-stat-subtext">Unmounted tenant claims</div>
      </div>
    </div>

    <h2 style="font-size: 15px; margin: 20px 0 10px;">PostgreSQL Instances (CloudNativePG)</h2>
    <div class="lens-table-container" style="margin-bottom: 24px;">
      <table class="lens-table">
        <thead><tr><th>Cluster Name</th><th>Namespace</th><th>Status</th><th>Primary (Read/Write)</th><th>Standbys</th><th>Backup State</th><th>Age</th></tr></thead>
        <tbody>
          ${!topo.postgres_clusters || topo.postgres_clusters.length === 0 ? `<tr><td colspan="7" class="lens-empty-state">No CloudNativePG database clusters provisioned yet.</td></tr>` : ""}
          ${(topo.postgres_clusters || []).map((c) => `
            <tr>
              <td class="lens-table-name">🐘 ${escapeHTML(c.name)}</td>
              <td class="lens-table-ns">${escapeHTML(c.namespace)}</td>
              <td><span class="lens-badge lens-badge-ready"><span class="lens-badge-dot"></span>${escapeHTML(c.status)}</span></td>
              <td><strong>${escapeHTML(c.primary || `${c.name}-1`)}</strong></td>
              <td class="lens-table-ns">${escapeHTML((c.replicas || []).join(", "))}</td>
              <td><span class="lens-badge lens-badge-completed">WAL Active</span></td>
              <td class="lens-table-age">${escapeHTML(c.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>

    <h2 style="font-size: 15px; margin: 20px 0 10px;">MySQL InnoDB Clusters</h2>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Cluster Name</th><th>Namespace</th><th>Status</th><th>Primary (R/W)</th><th>Replicas</th><th>Backup State</th><th>Age</th></tr></thead>
        <tbody>
          ${!topo.mysql_clusters || topo.mysql_clusters.length === 0 ? `<tr><td colspan="7" class="lens-empty-state">No MySQL InnoDB clusters provisioned yet.</td></tr>` : ""}
          ${(topo.mysql_clusters || []).map((m) => `
            <tr>
              <td class="lens-table-name">🐬 ${escapeHTML(m.name)}</td>
              <td class="lens-table-ns">${escapeHTML(m.namespace)}</td>
              <td><span class="lens-badge lens-badge-ready"><span class="lens-badge-dot"></span>${escapeHTML(m.status)}</span></td>
              <td><strong>${escapeHTML(m.primary || `${m.name}-0`)}</strong></td>
              <td class="lens-table-ns">${escapeHTML((m.replicas || []).join(", "))}</td>
              <td><span class="lens-badge lens-badge-completed">Schedule Active</span></td>
              <td class="lens-table-age">${escapeHTML(m.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderStorageLeaks(container) {
  const topo = await request("/api/v1/admin/databases/topology");
  const leaks = topo.storage_leaks || [];

  container.innerHTML = `
    <div class="lens-view-header">
      <div class="lens-view-title">
        <h1>💾 Orphan Storage & Volume Leak Detector</h1>
        <span class="lens-view-count">${leaks.length}</span>
      </div>
      <div class="lens-view-actions">
        <button class="lens-btn lens-btn-primary" onclick="renderStorageLeaks($('#lens-content-area'))">⟳ Scan for Leaks</button>
      </div>
    </div>
    <p style="color: var(--lens-text-muted); font-size: 12px; margin-bottom: 16px;">
      Persistent Volume Claims (PVCs) created by customer apps that are currently unmounted and retained after workload deletion. Purging frees underlying cloud storage disk resources.
    </p>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Claim Name</th><th>Namespace</th><th>Capacity</th><th>StorageClass</th><th>Reason</th><th>Age</th><th style="text-align: right;">Purge</th></tr></thead>
        <tbody>
          ${leaks.length === 0 ? `<tr><td colspan="7" class="lens-empty-state">🎉 Clean cluster! No orphaned persistent volume claims detected.</td></tr>` : ""}
          ${leaks.map((item) => `
            <tr>
              <td class="lens-table-name">💾 ${escapeHTML(item.pvc_name)}</td>
              <td class="lens-table-ns">${escapeHTML(item.namespace)}</td>
              <td><strong>${escapeHTML(item.capacity || "5Gi")}</strong></td>
              <td>${escapeHTML(item.storage_class || "standard")}</td>
              <td style="color: var(--lens-amber); font-size: 11px;">${escapeHTML(item.reason)}</td>
              <td class="lens-table-age">${escapeHTML(item.age)}</td>
              <td style="text-align: right;">
                <button class="lens-btn lens-btn-danger lens-btn-sm" onclick="deletePVC('${escapeHTML(item.namespace)}', '${escapeHTML(item.pvc_name)}')">Purge Volume</button>
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function renderEfficiency(container) {
  const eff = await request("/api/v1/admin/resources/efficiency");

  container.innerHTML = `
    <div class="lens-view-header">
      <div class="lens-view-title"><h1>📊 Cluster Resource Efficiency & Heatmap</h1></div>
      <button class="lens-btn lens-btn-primary" onclick="renderEfficiency($('#lens-content-area'))">⟳ Recalculate</button>
    </div>

    <div class="lens-overview-grid">
      <div class="lens-stat-card">
        <div class="lens-stat-header"><span>CPU Requested</span><span>Total</span></div>
        <div class="lens-stat-value">${escapeHTML(eff.cpu_requests_total)}</div>
        <div class="lens-progress-wrap">
          <div class="lens-progress-bar">
            <div class="lens-progress-fill ${eff.cpu_utilization_percent > 80 ? "red" : "green"}" style="width: ${Math.min(eff.cpu_utilization_percent, 100)}%;"></div>
          </div>
        </div>
        <div class="lens-stat-subtext">${eff.cpu_utilization_percent.toFixed(1)}% of ${escapeHTML(eff.total_cpu_allocatable)} allocatable</div>
      </div>

      <div class="lens-stat-card">
        <div class="lens-stat-header"><span>Memory Requested</span><span>Total</span></div>
        <div class="lens-stat-value">${escapeHTML(eff.mem_requests_total)}</div>
        <div class="lens-progress-wrap">
          <div class="lens-progress-bar">
            <div class="lens-progress-fill ${eff.mem_utilization_percent > 80 ? "red" : "green"}" style="width: ${Math.min(eff.mem_utilization_percent, 100)}%;"></div>
          </div>
        </div>
        <div class="lens-stat-subtext">${eff.mem_utilization_percent.toFixed(1)}% of ${escapeHTML(eff.total_mem_allocatable)} allocatable</div>
      </div>
    </div>

    <h2 style="font-size: 15px; margin: 20px 0 10px;">Workspace Allocation Breakdown</h2>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Workspace</th><th>Namespace</th><th>Pods</th><th>CPU Request</th><th>CPU Limit</th><th>Mem Request</th><th>Mem Limit</th><th>Efficiency</th></tr></thead>
        <tbody>
          ${(eff.workspaces || []).length === 0 ? `<tr><td colspan="8" class="lens-empty-state">No active workspace workloads found.</td></tr>` : ""}
          ${(eff.workspaces || []).map((w) => `
            <tr>
              <td class="lens-table-name"><span class="lens-ws-tag">${escapeHTML(w.workspace_name)}</span></td>
              <td class="lens-table-ns">${escapeHTML(w.namespace)}</td>
              <td>${w.pods_count}</td>
              <td>${escapeHTML(w.cpu_request)}</td>
              <td>${escapeHTML(w.cpu_limit)}</td>
              <td>${escapeHTML(w.mem_request)}</td>
              <td>${escapeHTML(w.mem_limit)}</td>
              <td><span class="lens-badge ${w.efficiency === "Optimal" ? "lens-badge-ready" : "lens-badge-warning"}">${escapeHTML(w.efficiency)}</span></td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

// -------------------------------------------------------------
// 10. Platform Operations: Connect, Readiness, Backup
// -------------------------------------------------------------

function renderClusterConnect(container) {
  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>Connect Deployment Cluster</h1></div></div>
    <div style="max-width: 720px; background: var(--lens-surface); border: 1px solid var(--lens-border); border-radius: 8px; padding: 24px;">
      <p style="color: var(--lens-text-muted); font-size: 12px; margin-top: 0;">
        Connect the Kubernetes cluster that customer workspaces deploy to. Paste a self-contained kubeconfig with certificate data embedded.
      </p>
      <form id="lens-connect-form">
        <label class="form-label" for="cluster-kubeconfig">Kubeconfig YAML</label>
        <textarea class="text-input" id="cluster-kubeconfig" rows="8" placeholder="apiVersion: v1&#10;clusters:&#10;..." required style="width: 100%; font-family: var(--lens-mono); font-size: 11px;"></textarea>
        
        <label class="form-label" for="cluster-context" style="margin-top: 14px;">Context (optional)</label>
        <input class="text-input" id="cluster-context" placeholder="current-context" style="width: 100%;">
        
        <p id="cluster-connect-state" style="color: var(--lens-text-muted); font-size: 12px; margin: 12px 0;">No new connection attempt.</p>
        <button class="lens-btn lens-btn-primary" id="cluster-connect-submit" type="submit">Validate and Connect Cluster</button>
      </form>
    </div>`;

  $("#lens-connect-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const btn = $("#cluster-connect-submit");
    const st = $("#cluster-connect-state");
    btn.disabled = true;
    st.textContent = "Connecting to Kubernetes API server…";
    try {
      const res = await fetch("/api/v1/admin/clusters/connect", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          kubeconfig: $("#cluster-kubeconfig").value,
          context: $("#cluster-context").value.trim(),
        }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || "Connection failed");
      st.innerHTML = `<span style="color: var(--lens-green);">✓ Connected successfully to “${escapeHTML(data.context)}”! Active for customer workloads.</span>`;
      await loadClusterHeader();
      await loadNamespacesDropdown();
    } catch (err) {
      st.innerHTML = `<span style="color: var(--lens-red);">✕ ${escapeHTML(err.message)}</span>`;
    } finally {
      btn.disabled = false;
    }
  });
}

async function renderReadiness(container) {
  const readiness = await request("/api/v1/admin/readiness");

  container.innerHTML = `
    <div class="lens-view-header">
      <div class="lens-view-title"><h1>Cluster Readiness Checklist</h1></div>
      <button class="lens-btn lens-btn-primary" onclick="renderReadiness($('#lens-content-area'))">⟳ Re-check</button>
    </div>
    <div style="margin-bottom: 16px; padding: 12px 16px; border-radius: 6px; background: ${readiness.ready ? "var(--lens-green-bg)" : "var(--lens-amber-bg)"}; border: 1px solid ${readiness.ready ? "var(--lens-green-border)" : "var(--lens-amber-border)"};">
      <strong style="color: ${readiness.ready ? "var(--lens-green)" : "var(--lens-amber)"};">
        ${readiness.ready ? "✓ All core cluster prerequisites verified." : "⚠️ Some cluster components need attention before offering all managed database engines."}
      </strong>
    </div>
    <div class="lens-table-container">
      <table class="lens-table">
        <thead><tr><th>Check Name</th><th>Status</th><th>Details</th></tr></thead>
        <tbody>
          ${readiness.checks.map((c) => `
            <tr>
              <td class="lens-table-name">${escapeHTML(c.name)}</td>
              <td><span class="lens-badge ${c.ready ? "lens-badge-ready" : "lens-badge-warning"}">${c.ready ? "Ready" : "Needs Attention"}</span></td>
              <td style="color: var(--lens-text-muted); font-size: 12px;">${escapeHTML(c.detail)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

function renderBackup(container) {
  container.innerHTML = `
    <div class="lens-view-header"><div class="lens-view-title"><h1>Control-Plane Backup</h1></div></div>
    <div style="max-width: 600px; background: var(--lens-surface); border: 1px solid var(--lens-border); border-radius: 8px; padding: 24px;">
      <h3 style="margin-top: 0;">SQLite Control-Plane Database Snapshot</h3>
      <p style="color: var(--lens-text-muted); font-size: 12px;">
        Download a consistent point-in-time snapshot of the Crelan control-plane SQLite database (including workspaces, deployments, users, and audit records).
      </p>
      <button class="lens-btn lens-btn-primary" id="btn-do-backup" type="button" style="margin-top: 12px;">
        📥 Download Control-Plane Snapshot (.db)
      </button>
      <p id="backup-state-text" style="color: var(--lens-text-dim); font-size: 11px; margin-top: 10px;"></p>
    </div>`;

  $("#btn-do-backup").addEventListener("click", async () => {
    const btn = $("#btn-do-backup");
    const st = $("#backup-state-text");
    btn.disabled = true;
    st.textContent = "Creating database snapshot…";
    try {
      const res = await fetch("/api/v1/admin/control-plane-backup", { method: "POST", credentials: "same-origin" });
      if (!res.ok) throw new Error("Backup failed");
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = "crelan-control-plane.db";
      link.click();
      URL.revokeObjectURL(url);
      st.textContent = "Snapshot downloaded successfully.";
    } catch (e) {
      st.textContent = e.message;
    } finally {
      btn.disabled = false;
    }
  });
}

// -------------------------------------------------------------
// Slide-Over Resource Inspector
// -------------------------------------------------------------

async function openInspector(kind, namespace, name) {
  state.activeInspector = { kind, namespace, name };
  $("#drawer-kind").textContent = kind;
  $("#drawer-title").textContent = name;

  // Build toolbar buttons based on kind
  const toolbar = $("#drawer-toolbar");
  toolbar.replaceChildren();

  if (kind === "pod") {
    const btnDiag = createBtn("🤖 AI Diagnose", "btn-diagnose", () => openAIDiagnosis(namespace, name));
    const btnLogs = createBtn("📜 Logs", "", () => openPodLogs(namespace, name));
    const btnExec = createBtn("🖥️ Terminal", "", () => openPodTerminal(namespace, name));
    const btnDel = createBtn("🗑️ Delete", "lens-btn-danger", () => confirmDeletePod(namespace, name));
    toolbar.append(btnDiag, btnLogs, btnExec, btnDel);
  } else if (kind === "deployment") {
    const btnRestart = createBtn("🔄 Rollout Restart", "", () => triggerRolloutRestart(namespace, name));
    const btnScale = createBtn("⚖️ Scale", "", () => openScaleModal(namespace, name, 1));
    const btnDel = createBtn("🗑️ Delete", "lens-btn-danger", () => confirmDeleteDeployment(namespace, name));
    toolbar.append(btnRestart, btnScale, btnDel);
  }

  // Set active tab to Overview
  $$(".lens-drawer-tab").forEach((t) => t.classList.toggle("active", t.dataset.tab === "overview"));

  $("#lens-drawer-overlay").classList.add("open");
  $("#lens-inspector-drawer").classList.add("open");

  await renderInspectorTab("overview");
}

function createBtn(text, extraClass, handler) {
  const b = document.createElement("button");
  b.className = `lens-btn lens-btn-sm ${extraClass}`;
  b.textContent = text;
  b.onclick = handler;
  return b;
}

function closeInspector() {
  $("#lens-drawer-overlay").classList.remove("open");
  $("#lens-inspector-drawer").classList.remove("open");
  state.activeInspector = null;
}

async function renderInspectorTab(tab) {
  if (!state.activeInspector) return;
  const { kind, namespace, name } = state.activeInspector;
  const body = $("#drawer-body");
  body.innerHTML = `<div class="lens-empty-state">Loading ${tab}…</div>`;

  try {
    if (tab === "overview") {
      let raw;
      if (kind === "pod") raw = await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`);
      else if (kind === "deployment") raw = await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`);
      else if (kind === "node") raw = await request(`/api/v1/admin/nodes/${encodeURIComponent(name)}`);
      else if (kind === "secret") raw = await request(`/api/v1/admin/secrets/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`);
      else raw = await request(`/api/v1/admin/resource/yaml?kind=${encodeURIComponent(kind)}&namespace=${encodeURIComponent(namespace)}&name=${encodeURIComponent(name)}`);

      state.activeInspector.raw = raw;
      renderInspectorOverview(body, kind, raw);
    } else if (tab === "events") {
      const events = await request(`/api/v1/admin/events?namespace=${encodeURIComponent(namespace)}`);
      const scoped = events.filter((e) => e.object_name === name);
      body.innerHTML = `
        <div class="lens-table-container">
          <table class="lens-table">
            <thead><tr><th>Type</th><th>Reason</th><th>Message</th><th>Age</th></tr></thead>
            <tbody>
              ${scoped.length === 0 ? `<tr><td colspan="4" class="lens-empty-state">No events recorded for this resource.</td></tr>` : ""}
              ${scoped.map((e) => `
                <tr>
                  <td><span class="lens-badge ${e.type === "Warning" ? "lens-badge-warning" : "lens-badge-ready"}">${escapeHTML(e.type)}</span></td>
                  <td><strong>${escapeHTML(e.reason)}</strong></td>
                  <td>${escapeHTML(e.message)}</td>
                  <td>${escapeHTML(e.age)}</td>
                </tr>
              `).join("")}
            </tbody>
          </table>
        </div>`;
    } else if (tab === "yaml") {
      const raw = state.activeInspector.raw || await request(`/api/v1/admin/resource/yaml?kind=${encodeURIComponent(kind)}&namespace=${encodeURIComponent(namespace)}&name=${encodeURIComponent(name)}`);
      const jsonStr = JSON.stringify(raw, null, 2);
      body.innerHTML = `
        <div class="lens-yaml-wrap">
          <button class="lens-yaml-copy-btn" id="btn-copy-yaml">Copy JSON</button>
          <pre class="lens-yaml-code">${escapeHTML(jsonStr)}</pre>
        </div>`;
      $("#btn-copy-yaml").onclick = () => {
        navigator.clipboard.writeText(jsonStr);
        $("#btn-copy-yaml").textContent = "Copied!";
        setTimeout(() => ($("#btn-copy-yaml").textContent = "Copy JSON"), 2000);
      };
    }
  } catch (err) {
    body.innerHTML = `<div class="lens-empty-state" style="color: var(--lens-red);">Error: ${escapeHTML(err.message)}</div>`;
  }
}

function renderInspectorOverview(container, kind, raw) {
  const meta = raw.metadata || {};
  let specDetails = "";

  if (kind === "secret" && raw.data) {
    specDetails = `
      <div class="lens-spec-group">
        <div class="lens-spec-group-title">Decoded Secret Data (${raw.keys} keys)</div>
        ${Object.entries(raw.data).map(([k, v]) => `
          <div class="lens-spec-row">
            <span class="lens-spec-label">${escapeHTML(k)}</span>
            <span class="lens-spec-val" style="color: var(--lens-amber);">${escapeHTML(v)}</span>
          </div>
        `).join("")}
      </div>`;
  }

  container.innerHTML = `
    <div class="lens-spec-group">
      <div class="lens-spec-group-title">Resource Metadata</div>
      <div class="lens-spec-row"><span class="lens-spec-label">Name</span><span class="lens-spec-val">${escapeHTML(meta.name || raw.name)}</span></div>
      <div class="lens-spec-row"><span class="lens-spec-label">Namespace</span><span class="lens-spec-val">${escapeHTML(meta.namespace || raw.namespace || "-")}</span></div>
      <div class="lens-spec-row"><span class="lens-spec-label">UID</span><span class="lens-spec-val">${escapeHTML(meta.uid || "-")}</span></div>
      <div class="lens-spec-row"><span class="lens-spec-label">Created</span><span class="lens-spec-val">${escapeHTML(meta.creationTimestamp || raw.created_at || "-")}</span></div>
    </div>
    ${specDetails}
    <div class="lens-spec-group">
      <div class="lens-spec-group-title">Labels</div>
      ${meta.labels ? Object.entries(meta.labels).map(([k, v]) => `
        <div class="lens-spec-row"><span class="lens-spec-label">${escapeHTML(k)}</span><span class="lens-spec-val">${escapeHTML(v)}</span></div>
      `).join("") : `<p style="color: var(--lens-text-dim); font-size: 11px;">None</p>`}
    </div>
  `;
}

// -------------------------------------------------------------
// Interactive In-Browser Pod Exec / Terminal
// -------------------------------------------------------------

function openPodTerminal(namespace, podName, container = "") {
  state.activeTerminal = { pod: podName, namespace, container };
  $("#terminal-pod-name").textContent = podName;
  $("#terminal-container-name").textContent = container || "default";
  $("#terminal-prompt").textContent = `[${escapeHTML(podName)}]$`;
  $("#lens-terminal-drawer").classList.add("open");
  $("#terminal-input").focus();
}

function closePodTerminal() {
  $("#lens-terminal-drawer").classList.remove("open");
  state.activeTerminal = null;
}

async function runTerminalCommand(cmd) {
  if (!state.activeTerminal || !cmd.trim()) return;
  const out = $("#terminal-output");
  const { pod, namespace, container } = state.activeTerminal;

  out.textContent += `\n[sh]$ ${cmd}\nRunning...\n`;
  $("#terminal-body").scrollTop = $("#terminal-body").scrollHeight;

  try {
    const res = await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(pod)}/exec`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ command: cmd, container }),
    });

    let result = "";
    if (res.stdout) result += res.stdout;
    if (res.stderr) result += `\n[STDERR] ` + res.stderr;
    if (res.error) result += `\n[EXEC ERROR] ` + res.error;

    out.textContent += result + "\n";
  } catch (err) {
    out.textContent += `Error: ${err.message}\n`;
  }
  $("#terminal-body").scrollTop = $("#terminal-body").scrollHeight;
}

// -------------------------------------------------------------
// Pod Logs Viewer Drawer
// -------------------------------------------------------------

async function openPodLogs(namespace, podName) {
  state.activeLogs = { pod: podName, namespace, container: "" };
  $("#logs-pod-name").textContent = `${namespace}/${podName}`;
  $("#lens-logs-drawer").classList.add("open");

  // Load pod containers
  try {
    const pod = await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}`);
    const select = $("#logs-container-select");
    select.replaceChildren();
    if (pod.spec && pod.spec.containers) {
      pod.spec.containers.forEach((c) => {
        const opt = document.createElement("option");
        opt.value = c.name;
        opt.textContent = c.name;
        select.append(opt);
      });
    }
  } catch (e) {}

  await fetchAndDisplayLogs();
}

function closePodLogs() {
  $("#lens-logs-drawer").classList.remove("open");
  if (state.activeLogs && state.activeLogs.timer) {
    clearInterval(state.activeLogs.timer);
  }
  state.activeLogs = null;
}

async function fetchAndDisplayLogs() {
  if (!state.activeLogs) return;
  const { pod, namespace } = state.activeLogs;
  const container = $("#logs-container-select").value || "";
  const tail = $("#logs-tail-select").value || "250";
  const prev = $("#logs-previous-check").checked;

  try {
    const logs = await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(pod)}/logs?container=${encodeURIComponent(container)}&tail=${tail}&previous=${prev}`);
    const filter = ($("#logs-filter-input").value || "").toLowerCase();
    const out = $("#logs-output");

    if (filter) {
      const lines = logs.split("\n").filter((l) => l.toLowerCase().includes(filter));
      out.textContent = lines.join("\n");
    } else {
      out.textContent = logs || "No container log entries found.";
    }

    if ($("#logs-autoscroll-check").checked) {
      $("#logs-body").scrollTop = $("#logs-body").scrollHeight;
    }
  } catch (err) {
    $("#logs-output").textContent = `Error fetching logs: ${err.message}`;
  }
}

// -------------------------------------------------------------
// AI Diagnostic Copilot
// -------------------------------------------------------------

async function openAIDiagnosis(namespace, podName) {
  const modal = $("#lens-ai-modal");
  const body = $("#ai-modal-body");
  modal.classList.add("open");
  body.innerHTML = `<div class="lens-empty-state">Running root-cause diagnosis for ${escapeHTML(podName)}…</div>`;

  try {
    const diag = await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}/diagnose`);

    body.innerHTML = `
      <div class="lens-ai-banner">
        <div class="lens-ai-banner-title">
          <span>${diag.severity === "critical" ? "🚨" : diag.severity === "warning" ? "⚠️" : "✓"}</span>
          ${escapeHTML(diag.summary)}
        </div>
        <div style="font-size: 11px; color: var(--lens-text-muted);">Confidence: <strong>${escapeHTML(diag.confidence)}</strong> · Severity: <strong>${escapeHTML(diag.severity.toUpperCase())}</strong></div>
      </div>

      <div class="lens-ai-cause-box">
        <div class="lens-ai-cause-title">Probable Cause & Analysis</div>
        <p style="margin: 0; font-size: 12px; line-height: 1.6; color: var(--lens-text);">${escapeHTML(diag.probable_cause)}</p>
      </div>

      <div class="lens-spec-group">
        <div class="lens-spec-group-title">Diagnostic Evidence</div>
        <ul style="margin: 0; padding-left: 20px; font-size: 12px; color: var(--lens-text-muted);">
          ${diag.evidence.map((ev) => `<li style="margin-bottom: 4px;">${escapeHTML(ev)}</li>`).join("")}
        </ul>
      </div>

      <div class="lens-spec-group">
        <div class="lens-spec-group-title">Recommended Remediations</div>
        ${diag.remediation.map((act) => `
          <div class="lens-remediation-card">
            <div class="lens-remediation-info">
              <strong>${escapeHTML(act.title)}</strong>
              <span>${escapeHTML(act.description)}</span>
            </div>
            ${act.auto_fixable ? `<button class="lens-btn lens-btn-primary lens-btn-sm" onclick="runAutoFix('${escapeHTML(act.action_id)}', '${escapeHTML(namespace)}', '${escapeHTML(podName)}')">Apply Fix</button>` : ""}
          </div>
        `).join("")}
      </div>

      ${diag.relevant_logs ? `
        <div class="lens-spec-group">
          <div class="lens-spec-group-title">Recent Crash Logs (Last 50 lines)</div>
          <pre class="lens-yaml-code" style="max-height: 200px;">${escapeHTML(diag.relevant_logs)}</pre>
        </div>` : ""}
    `;
  } catch (err) {
    body.innerHTML = `<div class="lens-empty-state" style="color: var(--lens-red);">Diagnosis failed: ${escapeHTML(err.message)}</div>`;
  }
}

async function runAutoFix(actionId, namespace, podName) {
  try {
    if (actionId === "restart_pod") {
      await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}?force=true`, { method: "DELETE" });
      alert(`Pod ${podName} evicted. Controller is restarting container.`);
    } else if (actionId === "restart_deployment") {
      // Find deployment name from pod prefix
      const parts = podName.split("-");
      parts.pop(); // remove hash
      parts.pop(); // remove replica hash
      const depName = parts.join("-");
      await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(depName)}/restart`, { method: "POST" });
      alert(`Rollout restart triggered for deployment ${depName}.`);
    }
    $("#lens-ai-modal").classList.remove("open");
    renderCurrentView();
  } catch (err) {
    alert("Fix execution failed: " + err.message);
  }
}

// -------------------------------------------------------------
// Fast Remediation Actions (Restart, Scale, Delete)
// -------------------------------------------------------------

async function triggerRolloutRestart(namespace, deploymentName) {
  if (!confirm(`Trigger a rolling restart for deployment “${deploymentName}”?`)) return;
  try {
    await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(deploymentName)}/restart`, { method: "POST" });
    alert(`Rollout restart initiated for ${deploymentName}.`);
    renderCurrentView();
  } catch (err) {
    alert("Restart failed: " + err.message);
  }
}

function openScaleModal(namespace, name, currentReplicas) {
  $("#scale-dep-name").textContent = `${namespace}/${name}`;
  $("#scale-range").value = currentReplicas || 1;
  $("#scale-input").value = currentReplicas || 1;
  $("#scale-modal-form").dataset.ns = namespace;
  $("#scale-modal-form").dataset.name = name;
  $("#lens-scale-modal").classList.add("open");
}

function openCreateNamespaceModal() {
  $("#new-ns-name").value = "";
  $("#lens-create-ns-modal").classList.add("open");
  $("#new-ns-name").focus();
}

async function confirmDeletePod(namespace, podName) {
  if (!confirm(`Delete pod “${podName}”? (Kubernetes will reschedule if managed by deployment)`)) return;
  try {
    await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}`, { method: "DELETE" });
    closeInspector();
    renderCurrentView();
  } catch (err) {
    alert("Pod deletion failed: " + err.message);
  }
}

async function confirmDeleteDeployment(namespace, depName) {
  if (!confirm(`Are you sure you want to permanently delete deployment “${depName}”?`)) return;
  try {
    await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(depName)}`, { method: "DELETE" });
    closeInspector();
    renderCurrentView();
  } catch (err) {
    alert("Deployment deletion failed: " + err.message);
  }
}

async function deletePVC(namespace, name) {
  if (!confirm(`Delete persistent volume claim “${name}”? Any retained disk data will be deleted.`)) return;
  try {
    await request(`/api/v1/admin/pvcs/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`, { method: "DELETE" });
    renderCurrentView();
  } catch (err) {
    alert("Delete failed: " + err.message);
  }
}

async function deleteNamespace(name) {
  if (!confirm(`Permanently delete namespace “${name}” and all workloads within it?`)) return;
  try {
    await request(`/api/v1/admin/namespaces/${encodeURIComponent(name)}`, { method: "DELETE" });
    await loadNamespacesDropdown();
    renderCurrentView();
  } catch (err) {
    alert("Namespace deletion failed: " + err.message);
  }
}

// -------------------------------------------------------------
// Event Listeners & Initialization
// -------------------------------------------------------------

function setupEventListeners() {
  window.addEventListener("hashchange", renderCurrentView);

  // Topbar Namespace Select
  $("#lens-namespace-select").addEventListener("change", (e) => {
    state.selectedNamespace = e.target.value;
    renderCurrentView();
  });

  // Topbar Search Filter
  $("#lens-search-input").addEventListener("input", (e) => {
    state.searchQuery = e.target.value.trim();
    renderCurrentView();
  });

  // Hotkey "/" for search
  window.addEventListener("keydown", (e) => {
    if (e.key === "/" && document.activeElement.tagName !== "INPUT" && document.activeElement.tagName !== "TEXTAREA") {
      e.preventDefault();
      $("#lens-search-input").focus();
    }
  });

  // Perspective Toggle
  $("#perspective-k8s").addEventListener("click", () => {
    state.perspective = "k8s";
    $("#perspective-k8s").classList.add("active");
    $("#perspective-ws").classList.remove("active");
    renderCurrentView();
  });

  $("#perspective-ws").addEventListener("click", () => {
    state.perspective = "workspace";
    $("#perspective-ws").classList.add("active");
    $("#perspective-k8s").classList.remove("active");
    renderCurrentView();
  });

  // Refresh Button
  $("#lens-refresh-btn").addEventListener("click", async () => {
    const icon = $("#lens-refresh-icon");
    icon.style.display = "inline-block";
    icon.animate([{ transform: "rotate(0deg)" }, { transform: "rotate(360deg)" }], { duration: 600 });
    await Promise.all([loadClusterHeader(), renderCurrentView()]);
  });

  $("#rail-refresh-btn").addEventListener("click", () => $("#lens-refresh-btn").click());

  // Auto-Refresh
  $("#lens-auto-refresh-select").addEventListener("change", (e) => {
    if (state.autoRefreshTimer) clearInterval(state.autoRefreshTimer);
    const ms = parseInt(e.target.value, 10);
    if (ms > 0) {
      state.autoRefreshTimer = setInterval(() => {
        if (!state.activeTerminal) renderCurrentView();
      }, ms);
    }
  });

  // Drawer Tabs & Close
  $("#drawer-close-btn").addEventListener("click", closeInspector);
  $("#lens-drawer-overlay").addEventListener("click", closeInspector);
  $$(".lens-drawer-tab").forEach((btn) => {
    btn.addEventListener("click", () => {
      $$(".lens-drawer-tab").forEach((t) => t.classList.remove("active"));
      btn.classList.add("active");
      renderInspectorTab(btn.dataset.tab);
    });
  });

  // Terminal Controls
  $("#terminal-close-btn").addEventListener("click", closePodTerminal);
  $("#terminal-clear-btn").addEventListener("click", () => ($("#terminal-output").textContent = ""));
  $("#terminal-form").addEventListener("submit", (e) => {
    e.preventDefault();
    const inp = $("#terminal-input");
    const cmd = inp.value;
    inp.value = "";
    runTerminalCommand(cmd);
  });
  $$(".lens-quick-cmd-btn").forEach((btn) => {
    btn.addEventListener("click", () => runTerminalCommand(btn.dataset.cmd));
  });

  // Logs Controls
  $("#logs-close-btn").addEventListener("click", closePodLogs);
  $("#logs-container-select").addEventListener("change", fetchAndDisplayLogs);
  $("#logs-tail-select").addEventListener("change", fetchAndDisplayLogs);
  $("#logs-previous-check").addEventListener("change", fetchAndDisplayLogs);
  $("#logs-filter-input").addEventListener("input", fetchAndDisplayLogs);
  $("#logs-download-btn").addEventListener("click", () => {
    const text = $("#logs-output").textContent;
    const blob = new Blob([text], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${state.activeLogs.pod}.log`;
    a.click();
    URL.revokeObjectURL(url);
  });

  // AI Modal Close
  $("#ai-modal-close").addEventListener("click", () => $("#lens-ai-modal").classList.remove("open"));
  $("#ai-modal-dismiss").addEventListener("click", () => $("#lens-ai-modal").classList.remove("open"));

  // Scale Modal Range Sync
  $("#scale-range").addEventListener("input", (e) => ($("#scale-input").value = e.target.value));
  $("#scale-input").addEventListener("input", (e) => ($("#scale-range").value = e.target.value));
  $("#scale-modal-cancel").addEventListener("click", () => $("#lens-scale-modal").classList.remove("open"));
  $("#scale-modal-close").addEventListener("click", () => $("#lens-scale-modal").classList.remove("open"));
  $("#scale-modal-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const ns = e.target.dataset.ns;
    const name = e.target.dataset.name;
    const reps = parseInt($("#scale-input").value, 10);
    try {
      await request(`/api/v1/admin/deployments/${encodeURIComponent(ns)}/${encodeURIComponent(name)}/scale`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ replicas: reps }),
      });
      $("#lens-scale-modal").classList.remove("open");
      renderCurrentView();
    } catch (err) {
      alert("Scale failed: " + err.message);
    }
  });

  // Create Namespace Modal
  $("#create-ns-close").addEventListener("click", () => $("#lens-create-ns-modal").classList.remove("open"));
  $("#create-ns-cancel").addEventListener("click", () => $("#lens-create-ns-modal").classList.remove("open"));
  $("#create-ns-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const name = $("#new-ns-name").value.trim();
    try {
      await request("/api/v1/admin/namespaces", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      });
      $("#lens-create-ns-modal").classList.remove("open");
      await loadNamespacesDropdown();
      renderCurrentView();
    } catch (err) {
      alert("Namespace creation failed: " + err.message);
    }
  });

  // Sign out
  $("#admin-signout").addEventListener("click", async () => {
    await fetch("/auth/logout", { method: "POST", credentials: "same-origin" });
    window.location.assign("/login");
  });
}

async function initializeLens() {
  try {
    const user = await request("/api/v1/me");
    if (user.platform_admin !== true) {
      $("#lens-content-area").innerHTML = `
        <div class="lens-empty-state">
          <h2>Access Denied</h2>
          <p>Your account does not have platform administrator access.</p>
          <a class="lens-btn lens-btn-primary" href="/dashboard">Return to Customer Workspace</a>
        </div>`;
      return;
    }
    const name = user.name || user.email || "Admin";
    $("#admin-avatar").textContent = name.charAt(0).toUpperCase();

    setupEventListeners();
    await Promise.all([loadClusterHeader(), loadNamespacesDropdown(), loadWorkspaceMappings()]);
    await renderCurrentView();

    // Start 10s auto-refresh by default
    state.autoRefreshTimer = setInterval(() => {
      if (!state.activeTerminal) renderCurrentView();
    }, 10000);
  } catch (err) {
    if (err.message !== "Sign in required") {
      $("#lens-content-area").innerHTML = `<div class="lens-empty-state">Failed to initialize: ${escapeHTML(err.message)}</div>`;
    }
  }
}

initializeLens();
