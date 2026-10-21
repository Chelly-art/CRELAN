// ==========================================================================
// Crelan Platform Admin Console Client Engine
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
  activeInspector: null,
  activeTerminal: null,
  activeLogs: null,
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
    let errMsg = `Request failed (${response.status})`;
    if (isJSON && body && body.error) {
      errMsg = body.error;
    } else if (!isJSON && typeof body === "string") {
      if (body.includes("520: Web server is returning an unknown error")) {
        errMsg = "Cloudflare Error (520): Server connection closed or timed out.";
      } else if (body.includes("524: A timeout occurred")) {
        errMsg = "Cloudflare Error (524): Request timed out waiting for server response.";
      } else if (body.includes("<title>")) {
        const titleMatch = body.match(/<title>(.*?)<\/title>/i);
        errMsg = titleMatch ? titleMatch[1].trim() : `Server error (${response.status})`;
      } else {
        errMsg = body.slice(0, 180).trim();
      }
    }
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
  $$(".admin-tree-item").forEach((item) => {
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
    const isConn = Boolean(info && info.connected);
    $("#sidebar-cluster-name").textContent = isConn ? (info.context || "connected-cluster") : "No cluster";
    $("#sidebar-cluster-tag").textContent = isConn ? "Connected" : "Disconnected";
    $("#topbar-context-name").textContent = isConn ? (info.context || "connected-cluster") : "No cluster connected";
    $("#topbar-k8s-version").textContent = isConn ? (info.server_version || "") : "";

    const dot = $("#topbar-dot");
    const railDot = $("#rail-status-dot");
    const railBadge = $("#rail-cluster-badge");
    if (isConn) {
      dot.style.backgroundColor = "var(--admin-green)";
      railDot.style.backgroundColor = "var(--admin-green)";
      if (railBadge) railBadge.classList.add("active");
    } else {
      dot.style.backgroundColor = "var(--admin-red)";
      railDot.style.backgroundColor = "var(--admin-red)";
      if (railBadge) railBadge.classList.remove("active");
    }

    $("#badge-nodes").textContent = isConn ? (info.nodes_count || 0) : 0;
    $("#badge-pods").textContent = isConn ? (info.pods_count || 0) : 0;
    $("#badge-namespaces").textContent = isConn ? (info.namespaces_count || 0) : 0;
    $("#badge-events").textContent = isConn ? (info.warning_events_count || 0) : 0;
  } catch (err) {
    console.warn("Could not load cluster header:", err);
  }
}

async function loadNamespacesDropdown() {
  try {
    const list = await request("/api/v1/admin/namespaces");
    state.namespaces = list;
    const select = $("#admin-namespace-select");
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
  const container = $("#admin-content-area");
  container.innerHTML = `<div class="admin-empty-state">Loading ${state.activeView}…</div>`;

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
      case "copilot":
        await renderCopilot(container);
        break;
      case "security":
        await renderSecurityAudit(container);
        break;
      case "rbac":
        await renderRBAC(container);
        break;
      case "rightsizing":
        await renderRightSizing(container);
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
      case "war-room":
        await renderWarRoom(container);
        break;
      case "audit":
        await renderAuditTrail(container);
        break;
      case "upgrade-radar":
        await renderUpgradeRadar(container);
        break;
      case "topology":
        await renderNetworkTopology(container);
        break;
      case "isolation-matrix":
        await renderTenantIsolationMatrix(container);
        break;
      case "certificates":
        await renderCertificates(container);
        break;
      case "webhooks":
        await renderWebhooks(container);
        break;
      case "dangling-configs":
        await renderDanglingConfigs(container);
        break;
      case "event-storm":
        await renderEventStorm(container);
        break;
      case "autoscaling":
        await renderAutoscaling(container);
        break;
      case "gitops-drift":
        await renderGitOpsDrift(container);
        break;
      case "backup-radar":
        await renderDisasterRecoveryRadar(container);
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
      case "image-vulnerabilities":
        await renderImageVulnerabilities(container);
        break;
      case "helm-releases":
        await renderHelmReleases(container);
        break;
      case "crds":
        await renderCRDs(container);
        break;
      case "can-i":
        await renderCanI(container);
        break;
      case "cluster-fleet":
        await renderClusterFleet(container);
        break;
      default:
        await renderOverview(container);
    }
  } catch (err) {
    if (err.message && err.message.includes("No Kubernetes cluster is connected")) {
      container.innerHTML = `
        <div class="admin-view-header">
          <div class="admin-view-title"><h1 style="text-transform: capitalize;">${escapeHTML(state.activeView)}</h1></div>
        </div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 60px 20px;">
            <h2 style="font-size: 18px; margin-bottom: 8px;">No Kubernetes Cluster Connected</h2>
            <p style="color: var(--admin-text-muted); max-width: 480px; margin: 0 auto 18px;">
              Connect your Kubernetes cluster using a kubeconfig to inspect and manage nodes, pods, workloads, and cluster resources.
            </p>
            <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect Cluster</a>
          </div>
        </div>`;
      return;
    }
    container.innerHTML = `
      <div class="admin-empty-state">
        <h3 style="color: var(--admin-red);">Failed to load view</h3>
        <p>${escapeHTML(err.message)}</p>
        <button class="admin-btn admin-btn-primary" onclick="renderCurrentView()" style="margin-top: 10px;">Retry</button>
      </div>`;
  }
}

// -------------------------------------------------------------
// 1. Cluster Overview
// -------------------------------------------------------------

async function renderOverview(container) {
  const [info, nodes, pods] = await Promise.all([
    request("/api/v1/admin/cluster/info").catch(() => ({ connected: false })),
    request("/api/v1/admin/nodes").catch(() => []),
    request("/api/v1/admin/pods").catch(() => []),
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
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Cluster Overview</h1>
        <span class="admin-view-count">${escapeHTML(info.context || "in-cluster")}</span>
      </div>
      <div class="admin-view-actions">
        <a class="admin-btn" href="#security">Security Audit</a>
        <a class="admin-btn" href="#efficiency">Resource Heatmap</a>
        <a class="admin-btn admin-btn-primary" href="#cluster-connect">Switch / Connect Cluster</a>
      </div>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Cluster Health</span><span>Platform</span></div>
        <div class="admin-stat-value" style="color: ${info.ready_nodes > 0 ? "var(--admin-green)" : "var(--admin-red)"};">
          ${info.connected ? "Healthy" : "Offline"}
        </div>
        <div class="admin-stat-subtext">${escapeHTML(info.server_version || "Kubernetes")} (${escapeHTML(info.platform || "linux/amd64")})</div>
      </div>

      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Ready Nodes</span><span>Infrastructure</span></div>
        <div class="admin-stat-value">${info.ready_nodes} / ${info.nodes_count}</div>
        <div class="admin-progress-wrap">
          <div class="admin-progress-bar">
            <div class="admin-progress-fill green" style="width: ${info.nodes_count > 0 ? (info.ready_nodes / info.nodes_count) * 100 : 0}%;"></div>
          </div>
        </div>
      </div>

      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Workloads (Pods)</span><span>Workloads</span></div>
        <div class="admin-stat-value">${pods.length}</div>
        <div class="admin-breakdown-bar">
          <div class="admin-breakdown-segment running" style="width: ${runPct}%;" title="Running: ${runningPods}"></div>
          <div class="admin-breakdown-segment pending" style="width: ${pendPct}%;" title="Pending: ${pendingPods}"></div>
          <div class="admin-breakdown-segment failed" style="width: ${failPct}%;" title="Failed: ${failedPods}"></div>
        </div>
        <div class="admin-stat-subtext">${runningPods} running · ${failedPods} with errors</div>
      </div>

      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Customer Workspaces</span><span>Tenants</span></div>
        <div class="admin-stat-value">${info.workspaces_count || 0}</div>
        <div class="admin-stat-subtext">${info.deployments_count || 0} active deployments</div>
      </div>
    </div>

    <!-- Cluster Nodes Summary -->
    <div style="margin-bottom: 24px;">
      <div class="admin-view-header">
        <div class="admin-view-title"><h2 style="font-size: 15px; margin: 0;">Cluster Nodes</h2></div>
        <a class="admin-btn admin-btn-sm" href="#nodes">View All Nodes</a>
      </div>
      <div class="admin-table-container">
        <table class="admin-table">
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
                <td class="admin-table-name">${escapeHTML(n.name)}</td>
                <td><span class="admin-badge ${n.status === "Ready" ? "admin-badge-ready" : "admin-badge-error"}"><span class="admin-badge-dot"></span>${escapeHTML(n.status)}</span></td>
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
      <div class="admin-view-header">
        <div class="admin-view-title"><h2 style="font-size: 15px; margin: 0;">Recent Cluster Warning Events</h2></div>
        <a class="admin-btn admin-btn-sm" href="#events">View All Events</a>
      </div>
      <div id="overview-events-container">
        <div class="admin-empty-state" style="padding: 20px;">Loading warning events…</div>
      </div>
    </div>
  `;
  container.innerHTML = html;

  try {
    const events = await request("/api/v1/admin/events?type=Warning");
    const evContainer = $("#overview-events-container");
    if (!events.length) {
      evContainer.innerHTML = `<div class="admin-empty-state" style="padding: 20px;">No warning events in the cluster. All workloads operating normally.</div>`;
    } else {
      evContainer.innerHTML = `
        <div class="admin-table-container">
          <table class="admin-table">
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
              ${events.slice(0, 8).map((e) => `
                <tr>
                  <td><span class="admin-badge admin-badge-warning"><span class="admin-badge-dot"></span>${escapeHTML(e.reason)}</span></td>
                  <td><strong>${escapeHTML(e.object_kind)}/${escapeHTML(e.object_name)}</strong></td>
                  <td class="admin-table-ns">${escapeHTML(e.namespace || "-")}</td>
                  <td style="max-width: 400px; overflow: hidden; text-overflow: ellipsis;">${escapeHTML(e.message)}</td>
                  <td class="admin-table-age">${escapeHTML(e.age)}</td>
                </tr>
              `).join("")}
            </tbody>
          </table>
        </div>`;
    }
  } catch (e) {}
}

// -------------------------------------------------------------
// 2. Nodes View
// -------------------------------------------------------------

async function renderNodes(container) {
  const nodes = await request("/api/v1/admin/nodes");
  const filtered = filterRows(nodes, ["name", "status", "internal_ip", "version"]);

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Nodes</h1>
        <span class="admin-view-count">${nodes.length}</span>
      </div>
    </div>
    <div class="admin-table-container">
      <table class="admin-table">
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
            <th style="text-align: right;">Actions</th>
          </tr>
        </thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="11" class="admin-empty-state">No nodes match your filter.</td></tr>` : ""}
          ${filtered.map((n) => `
            <tr onclick="openInspector('node', '', '${escapeHTML(n.name)}')">
              <td class="admin-table-name">${escapeHTML(n.name)}</td>
              <td><span class="admin-badge ${n.status === "Ready" ? "admin-badge-ready" : "admin-badge-error"}"><span class="admin-badge-dot"></span>${escapeHTML(n.status)}</span></td>
              <td>${escapeHTML(n.roles.join(", "))}</td>
              <td class="admin-table-ns">${escapeHTML(n.version)}</td>
              <td style="font-size: 11px; color: var(--admin-text-muted);">${escapeHTML(n.os_image)}</td>
              <td class="admin-table-ns">${escapeHTML(n.internal_ip)}</td>
              <td>${n.pods_running} / ${n.pods_capacity}</td>
              <td>${escapeHTML(n.cpu_allocatable)}</td>
              <td>${escapeHTML(n.mem_allocatable)}</td>
              <td class="admin-table-age">${escapeHTML(n.age)}</td>
              <td>
                <div class="admin-row-actions" style="justify-content: flex-end;">
                  <button class="admin-action-btn" onclick="event.stopPropagation(); openNodeDrainModal('${escapeHTML(n.name)}')" title="Node Drain & Maintenance Safety Simulator">Drain Sim</button>
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
// 3. Workloads: Pods View
// -------------------------------------------------------------

async function renderPods(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  let pods = [];
  try {
    pods = await request(`/api/v1/admin/pods${ns}`);
  } catch (err) {
    if (err.message && err.message.includes("No Kubernetes cluster is connected")) {
      container.innerHTML = `
        <div class="admin-view-header">
          <div class="admin-view-title"><h1>Pods</h1></div>
        </div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 16px;">
            <h3 style="font-size: 16px; margin-bottom: 8px;">No Kubernetes Cluster Connected</h3>
            <p style="color: var(--admin-text-muted); max-width: 440px; margin: 0 auto 16px;">
              Connect your Kubernetes cluster using a kubeconfig to inspect and manage pods and workloads.
            </p>
            <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect Cluster</a>
          </div>
        </div>
      `;
      return;
    }
    throw err;
  }
  const filtered = filterRows(pods, ["name", "namespace", "phase", "node", "pod_ip", "workspace_name", "app_name"]);

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Pods</h1>
        <span class="admin-view-count">${pods.length}</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" onclick="renderPods($('#admin-content-area'))">Refresh</button>
      </div>
    </div>
    <div class="admin-table-container">
      <table class="admin-table">
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
          ${filtered.length === 0 ? `<tr><td colspan="10" class="admin-empty-state">No pods found in namespace.</td></tr>` : ""}
          ${filtered.map((p) => {
            let badgeClass = "admin-badge-running";
            if (p.phase === "Pending") badgeClass = "admin-badge-pending";
            if (p.has_errors || p.phase === "CrashLoopBackOff" || p.phase === "Failed" || p.phase === "OOMKilled" || p.phase === "Error") badgeClass = "admin-badge-crashloop";
            if (p.phase === "Succeeded" || p.phase === "Completed") badgeClass = "admin-badge-completed";

            return `
              <tr class="${p.has_errors ? "row-error" : ""}">
                <td class="admin-table-name" onclick="openInspector('pod', '${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')">
                  ${escapeHTML(p.name)}
                </td>
                <td class="admin-table-ns">${escapeHTML(p.namespace)}</td>
                ${state.perspective === "workspace" ? `
                  <td>
                    ${p.workspace_name ? `<span class="admin-ws-tag">${escapeHTML(p.workspace_name)}</span>` : `<span style="color: var(--admin-text-dim);">-</span>`}
                    ${p.app_name ? `<small style="display:block; color: var(--admin-text-muted);">${escapeHTML(p.app_name)}</small>` : ""}
                  </td>` : ""}
                <td>${escapeHTML(p.ready_containers)}</td>
                <td style="color: ${p.restarts > 0 ? "var(--admin-amber)" : "inherit"}; font-weight: ${p.restarts > 0 ? "600" : "normal"};">${p.restarts}</td>
                <td class="admin-table-ns">${escapeHTML(p.controlled_by || "-")}</td>
                <td class="admin-table-ns">${escapeHTML(p.node || "-")}</td>
                <td><span class="admin-badge ${badgeClass}"><span class="admin-badge-dot"></span>${escapeHTML(p.phase)}</span></td>
                <td class="admin-table-age">${escapeHTML(p.age)}</td>
                <td>
                  <div class="admin-row-actions" style="justify-content: flex-end;">
                    <button class="admin-action-btn" onclick="event.stopPropagation(); openAIDiagnosis('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Run Root-Cause Diagnosis">Diagnose</button>
                    <button class="admin-action-btn" onclick="event.stopPropagation(); openPostMortem('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Generate Post-Mortem & Runbook">Post-Mortem</button>
                    <a class="admin-action-btn" href="#war-room?ns=${encodeURIComponent(p.namespace)}&pod=${encodeURIComponent(p.name)}" onclick="event.stopPropagation();" title="Open Incident War Room">War Room</a>
                    <button class="admin-action-btn" onclick="event.stopPropagation(); openPodLogs('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="View Container Logs">Logs</button>
                    <button class="admin-action-btn" onclick="event.stopPropagation(); openPodTerminal('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Interactive Terminal">Exec</button>
                    <button class="admin-action-btn" onclick="event.stopPropagation(); openEphemeralDebugModal('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Inject Ephemeral Debug Container (netshoot, alpine, etc)">Debug</button>
                    <button class="admin-action-btn btn-delete" onclick="event.stopPropagation(); confirmDeletePod('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Delete Pod">Delete</button>
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
// 4. Workloads: Deployments View
// -------------------------------------------------------------

async function renderDeployments(container) {
  const ns = state.selectedNamespace ? `?namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const deps = await request(`/api/v1/admin/deployments${ns}`);
  const filtered = filterRows(deps, ["name", "namespace", "workspace_name", "app_name"]);

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Deployments</h1>
        <span class="admin-view-count">${deps.length}</span>
      </div>
    </div>
    <div class="admin-table-container">
      <table class="admin-table">
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
          ${filtered.length === 0 ? `<tr><td colspan="9" class="admin-empty-state">No deployments found.</td></tr>` : ""}
          ${filtered.map((d) => `
            <tr>
              <td class="admin-table-name" onclick="openInspector('deployment', '${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')">
                ${escapeHTML(d.name)}
              </td>
              <td class="admin-table-ns">${escapeHTML(d.namespace)}</td>
              ${state.perspective === "workspace" ? `<td>${d.workspace_name ? `<span class="admin-ws-tag">${escapeHTML(d.workspace_name)}</span>` : "-"}</td>` : ""}
              <td><strong>${d.ready_replicas} / ${d.replicas}</strong></td>
              <td>${d.available_replicas}</td>
              <td class="admin-table-ns" style="max-width: 250px; overflow: hidden; text-overflow: ellipsis;">${escapeHTML(d.images.join(", "))}</td>
              <td>${escapeHTML(d.conditions.join(", ") || "Progressing")}</td>
              <td class="admin-table-age">${escapeHTML(d.age)}</td>
              <td>
                <div class="admin-row-actions" style="justify-content: flex-end;">
                  <button class="admin-action-btn" onclick="event.stopPropagation(); openRollbackModal('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')" title="Deployment Revision History & Visual Diff">Rollback</button>
                  <button class="admin-action-btn" onclick="event.stopPropagation(); triggerRolloutRestart('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')" title="Rollout Restart">Restart</button>
                  <button class="admin-action-btn" onclick="event.stopPropagation(); openScaleModal('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}', ${d.replicas})" title="Scale Replicas">Scale</button>
                  <button class="admin-action-btn btn-delete" onclick="event.stopPropagation(); confirmDeleteDeployment('${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')" title="Delete Deployment">Delete</button>
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
    <div class="admin-view-header"><div class="admin-view-title"><h1>StatefulSets</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Replicas</th><th>Images</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="5" class="admin-empty-state">No statefulsets found.</td></tr>` : ""}
          ${filtered.map((s) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(s.name)}</td>
              <td class="admin-table-ns">${escapeHTML(s.namespace)}</td>
              <td><strong>${s.ready_replicas} / ${s.replicas}</strong></td>
              <td class="admin-table-ns">${escapeHTML(s.images.join(", "))}</td>
              <td class="admin-table-age">${escapeHTML(s.age)}</td>
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
    <div class="admin-view-header"><div class="admin-view-title"><h1>DaemonSets</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Desired</th><th>Current</th><th>Ready</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="6" class="admin-empty-state">No daemonsets found.</td></tr>` : ""}
          ${filtered.map((d) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(d.name)}</td>
              <td class="admin-table-ns">${escapeHTML(d.namespace)}</td>
              <td>${d.desired}</td>
              <td>${d.current}</td>
              <td>${d.ready}</td>
              <td class="admin-table-age">${escapeHTML(d.age)}</td>
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
    <div class="admin-view-header"><div class="admin-view-title"><h1>Jobs</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Completions</th><th>Duration</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="5" class="admin-empty-state">No batch jobs found.</td></tr>` : ""}
          ${filtered.map((j) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(j.name)}</td>
              <td class="admin-table-ns">${escapeHTML(j.namespace)}</td>
              <td>${j.succeeded} / ${j.completions}</td>
              <td>${escapeHTML(j.duration || "-")}</td>
              <td class="admin-table-age">${escapeHTML(j.age)}</td>
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
    <div class="admin-view-header"><div class="admin-view-title"><h1>CronJobs</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Schedule</th><th>Suspend</th><th>Active</th><th>Last Schedule</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No cronjobs found.</td></tr>` : ""}
          ${filtered.map((c) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(c.name)}</td>
              <td class="admin-table-ns">${escapeHTML(c.namespace)}</td>
              <td><code>${escapeHTML(c.schedule)}</code></td>
              <td>${c.suspend ? "Yes" : "No"}</td>
              <td>${c.active}</td>
              <td>${escapeHTML(c.last_schedule || "-")}</td>
              <td class="admin-table-age">${escapeHTML(c.age)}</td>
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
    <div class="admin-view-header"><div class="admin-view-title"><h1>Services</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Type</th><th>Cluster IP</th><th>External IP</th><th>Ports</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No services found.</td></tr>` : ""}
          ${filtered.map((s) => `
            <tr onclick="openInspector('service', '${escapeHTML(s.namespace)}', '${escapeHTML(s.name)}')">
              <td class="admin-table-name">${escapeHTML(s.name)}</td>
              <td class="admin-table-ns">${escapeHTML(s.namespace)}</td>
              <td><span class="admin-badge admin-badge-completed">${escapeHTML(s.type)}</span></td>
              <td class="admin-table-ns">${escapeHTML(s.cluster_ip)}</td>
              <td>${escapeHTML(s.external_ips.join(", ") || "-")}</td>
              <td>${escapeHTML(s.ports.join(", "))}</td>
              <td class="admin-table-age">${escapeHTML(s.age)}</td>
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
    <div class="admin-view-header"><div class="admin-view-title"><h1>Ingresses</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Class</th><th>Hosts</th><th>Addresses</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="6" class="admin-empty-state">No ingresses found.</td></tr>` : ""}
          ${filtered.map((i) => `
            <tr onclick="openInspector('ingress', '${escapeHTML(i.namespace)}', '${escapeHTML(i.name)}')">
              <td class="admin-table-name">${escapeHTML(i.name)}</td>
              <td class="admin-table-ns">${escapeHTML(i.namespace)}</td>
              <td>${escapeHTML(i.ingress_class || "-")}</td>
              <td>${escapeHTML(i.hosts.join(", ") || "*")}</td>
              <td>${escapeHTML(i.addresses.join(", ") || "-")}</td>
              <td class="admin-table-age">${escapeHTML(i.age)}</td>
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
    <div class="admin-view-header"><div class="admin-view-title"><h1>Network Policies</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Pod Selector</th><th>Policy Types</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="5" class="admin-empty-state">No network policies found.</td></tr>` : ""}
          ${filtered.map((np) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(np.name)}</td>
              <td class="admin-table-ns">${escapeHTML(np.namespace)}</td>
              <td><code>${escapeHTML(np.pod_selector || "All pods")}</code></td>
              <td>${escapeHTML(np.policy_types.join(", "))}</td>
              <td class="admin-table-age">${escapeHTML(np.age)}</td>
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
    <div class="admin-view-header"><div class="admin-view-title"><h1>ConfigMaps</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Keys</th><th>Data Keys</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="5" class="admin-empty-state">No configmaps found.</td></tr>` : ""}
          ${filtered.map((cm) => `
            <tr onclick="openInspector('configmap', '${escapeHTML(cm.namespace)}', '${escapeHTML(cm.name)}')">
              <td class="admin-table-name">${escapeHTML(cm.name)}</td>
              <td class="admin-table-ns">${escapeHTML(cm.namespace)}</td>
              <td>${cm.keys_count}</td>
              <td class="admin-table-ns">${escapeHTML(cm.keys.slice(0, 5).join(", "))}${cm.keys.length > 5 ? "…" : ""}</td>
              <td class="admin-table-age">${escapeHTML(cm.age)}</td>
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
    <div class="admin-view-header"><div class="admin-view-title"><h1>Secrets</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Type</th><th>Keys</th><th>Data Keys</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="6" class="admin-empty-state">No secrets found.</td></tr>` : ""}
          ${filtered.map((s) => `
            <tr onclick="openInspector('secret', '${escapeHTML(s.namespace)}', '${escapeHTML(s.name)}')">
              <td class="admin-table-name">${escapeHTML(s.name)}</td>
              <td class="admin-table-ns">${escapeHTML(s.namespace)}</td>
              <td><span class="admin-badge admin-badge-completed">${escapeHTML(s.type)}</span></td>
              <td>${s.keys_count}</td>
              <td class="admin-table-ns">${escapeHTML(s.keys.slice(0, 5).join(", "))}${s.keys.length > 5 ? "…" : ""}</td>
              <td class="admin-table-age">${escapeHTML(s.age)}</td>
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
    <div class="admin-view-header"><div class="admin-view-title"><h1>Persistent Volume Claims</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Status</th><th>Capacity</th><th>StorageClass</th><th>Volume</th><th>Age</th><th style="text-align: right;">Action</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="8" class="admin-empty-state">No volume claims found.</td></tr>` : ""}
          ${filtered.map((p) => `
            <tr onclick="openInspector('pvc', '${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')">
              <td class="admin-table-name">${escapeHTML(p.name)}</td>
              <td class="admin-table-ns">${escapeHTML(p.namespace)}</td>
              <td><span class="admin-badge ${p.status === "Bound" ? "admin-badge-ready" : "admin-badge-warning"}">${escapeHTML(p.status)}</span></td>
              <td>${escapeHTML(p.capacity || "-")}</td>
              <td>${escapeHTML(p.storage_class || "-")}</td>
              <td class="admin-table-ns">${escapeHTML(p.volume_name || "-")}</td>
              <td class="admin-table-age">${escapeHTML(p.age)}</td>
              <td style="text-align: right;">
                <button class="admin-action-btn btn-delete" onclick="event.stopPropagation(); deletePVC('${escapeHTML(p.namespace)}', '${escapeHTML(p.name)}')" title="Delete PVC">Delete</button>
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
    <div class="admin-view-header"><div class="admin-view-title"><h1>Persistent Volumes</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Capacity</th><th>Access Modes</th><th>Reclaim</th><th>Status</th><th>Claim</th><th>StorageClass</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="8" class="admin-empty-state">No persistent volumes found.</td></tr>` : ""}
          ${filtered.map((pv) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(pv.name)}</td>
              <td>${escapeHTML(pv.capacity)}</td>
              <td>${escapeHTML(pv.access_modes.join(", "))}</td>
              <td>${escapeHTML(pv.reclaim_policy)}</td>
              <td><span class="admin-badge ${pv.status === "Bound" ? "admin-badge-ready" : "admin-badge-warning"}">${escapeHTML(pv.status)}</span></td>
              <td class="admin-table-ns">${escapeHTML(pv.claim || "-")}</td>
              <td>${escapeHTML(pv.storage_class || "-")}</td>
              <td class="admin-table-age">${escapeHTML(pv.age)}</td>
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
    <div class="admin-view-header"><div class="admin-view-title"><h1>Storage Classes</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Provisioner</th><th>Reclaim Policy</th><th>Volume Binding</th><th>Expansion</th><th>Default</th><th>Age</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No storage classes found.</td></tr>` : ""}
          ${filtered.map((sc) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(sc.name)}</td>
              <td class="admin-table-ns">${escapeHTML(sc.provisioner)}</td>
              <td>${escapeHTML(sc.reclaim_policy || "-")}</td>
              <td>${escapeHTML(sc.volume_binding_mode || "-")}</td>
              <td>${sc.allow_volume_expansion ? "Yes" : "No"}</td>
              <td>${sc.is_default ? `<span class="admin-badge admin-badge-ready">Default</span>` : "-"}</td>
              <td class="admin-table-age">${escapeHTML(sc.age)}</td>
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
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>Namespaces</h1><span class="admin-view-count">${list.length}</span></div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-primary" onclick="openCreateNamespaceModal()">Create Namespace</button>
      </div>
    </div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Name</th><th>Status</th><th>Capsule Tenant</th><th>Workspace</th><th>Age</th><th style="text-align: right;">Action</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="6" class="admin-empty-state">No namespaces found.</td></tr>` : ""}
          ${filtered.map((ns) => `
            <tr>
              <td class="admin-table-name" onclick="selectNamespaceAndFilter('${escapeHTML(ns.name)}')">${escapeHTML(ns.name)}</td>
              <td><span class="admin-badge ${ns.status === "Active" ? "admin-badge-ready" : "admin-badge-warning"}">${escapeHTML(ns.status)}</span></td>
              <td>${ns.tenant ? `<span class="admin-badge admin-badge-completed">Capsule: ${escapeHTML(ns.tenant)}</span>` : `<span style="color: var(--admin-text-dim);">-</span>`}</td>
              <td>${ns.workspace_id ? `<span class="admin-ws-tag">WS #${escapeHTML(ns.workspace_id)}</span>` : "-"}</td>
              <td class="admin-table-age">${escapeHTML(ns.age)}</td>
              <td style="text-align: right;">
                ${ns.name !== "default" && ns.name !== "kube-system" ? `<button class="admin-action-btn btn-delete" onclick="deleteNamespace('${escapeHTML(ns.name)}')" title="Delete Namespace">Delete</button>` : ""}
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

function selectNamespaceAndFilter(name) {
  state.selectedNamespace = name;
  $("#admin-namespace-select").value = name;
  setRoute("pods");
}

async function renderEvents(container) {
  const ns = state.selectedNamespace ? `&namespace=${encodeURIComponent(state.selectedNamespace)}` : "";
  const list = await request(`/api/v1/admin/events?type=${ns}`);
  const filtered = filterRows(list, ["reason", "message", "object_name", "object_kind", "namespace"]);

  container.innerHTML = `
    <div class="admin-view-header"><div class="admin-view-title"><h1>Cluster Events</h1><span class="admin-view-count">${list.length}</span></div></div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Type</th><th>Reason</th><th>Involved Object</th><th>Namespace</th><th>Message</th><th>Count</th><th>Last Seen</th></tr></thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No events found.</td></tr>` : ""}
          ${filtered.map((e) => `
            <tr>
              <td><span class="admin-badge ${e.type === "Warning" ? "admin-badge-warning" : "admin-badge-completed"}">${escapeHTML(e.type)}</span></td>
              <td><strong>${escapeHTML(e.reason)}</strong></td>
              <td class="admin-table-ns"><strong>${escapeHTML(e.object_kind)}</strong>/${escapeHTML(e.object_name)}</td>
              <td class="admin-table-ns">${escapeHTML(e.namespace || "-")}</td>
              <td style="max-width: 450px; overflow: hidden; text-overflow: ellipsis;">${escapeHTML(e.message)}</td>
              <td>${e.count}</td>
              <td class="admin-table-age">${escapeHTML(e.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

// -------------------------------------------------------------
// 9. Platform Intelligence Views (Security, DB, Leaks, Efficiency)
// -------------------------------------------------------------

async function renderSecurityAudit(container) {
  const audit = await request("/api/v1/admin/security/audit");

  let scoreColor = "green";
  if (audit.score < 60) scoreColor = "red";
  else if (audit.score < 85) scoreColor = "amber";

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>Capsule Tenant Radar & CIS Security Scanner</h1></div>
      <div style="display: flex; gap: 8px;">
        <button class="admin-btn" onclick="setRoute('rbac')">RBAC Blast Radius</button>
        <button class="admin-btn admin-btn-primary" onclick="renderSecurityAudit($('#admin-content-area'))">Re-scan Cluster</button>
      </div>
    </div>

    <div class="admin-score-banner">
      <div class="admin-score-circle ${scoreColor}">${audit.score}%</div>
      <div class="admin-score-details">
        <h3>CIS Kubernetes Security Posture: ${audit.score >= 80 ? "Hardened" : "Hardening Recommended"}</h3>
        <p>${audit.total_pods_scanned} active workloads evaluated · ${audit.passed_checks} checks passed · ${audit.failed_checks} policy findings</p>
        <p style="margin-top: 6px;">Capsule Tenant Isolation: <strong style="color: ${audit.tenant_isolation.status === "Secure" ? "var(--admin-green)" : "var(--admin-amber)"};">${escapeHTML(audit.tenant_isolation.status)}</strong> (${audit.tenant_isolation.enforced_namespaces}/${audit.tenant_isolation.total_tenant_namespaces} tenant namespaces enforced with NetworkPolicy)</p>
      </div>
    </div>

    <div class="admin-overview-grid" style="margin-top: 20px;">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Critical Findings</span><span>CIS 5.2</span></div>
        <div class="admin-stat-value" style="color: ${audit.critical_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${audit.critical_count || 0}</div>
        <div class="admin-stat-subtext">Immediate privilege risks</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Secret Leak Guard</span><span>Tokens & Keys</span></div>
        <div class="admin-stat-value" style="color: ${audit.secret_leaks_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${audit.secret_leaks_count || 0}</div>
        <div class="admin-stat-subtext">Plaintext secrets in env/ConfigMaps</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Root Containers</span><span>UID 0</span></div>
        <div class="admin-stat-value" style="color: ${audit.root_count > 0 ? "var(--admin-amber)" : "var(--admin-green)"};">${audit.root_count || 0}</div>
        <div class="admin-stat-subtext">Workloads missing runAsNonRoot</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Privileged Pods</span><span>SecurityContext</span></div>
        <div class="admin-stat-value" style="color: ${audit.privileged_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${audit.privileged_count || 0}</div>
        <div class="admin-stat-subtext">Container privileged: true</div>
      </div>
    </div>

    <h2 style="font-size: 15px; margin: 24px 0 10px;">Security & Isolation Findings</h2>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Severity</th>
            <th>Rule</th>
            <th>Workload / Pod</th>
            <th>Namespace</th>
            <th>Description</th>
            <th>Remediation & Action</th>
          </tr>
        </thead>
        <tbody>
          ${audit.issues.length === 0 ? `<tr><td colspan="6" class="admin-empty-state">Zero security violations detected in active workloads.</td></tr>` : ""}
          ${audit.issues.map((i) => `
            <tr>
              <td><span class="admin-badge ${i.severity === "critical" ? "admin-badge-crashloop" : "admin-badge-warning"}">${escapeHTML(i.severity.toUpperCase())}</span></td>
              <td><strong>${escapeHTML(i.rule)}</strong></td>
              <td class="admin-table-name" onclick="openInspector('pod', '${escapeHTML(i.namespace)}', '${escapeHTML(i.pod_name)}')">${escapeHTML(i.pod_name || i.workload_name || "-")}</td>
              <td class="admin-table-ns">${escapeHTML(i.namespace)}</td>
              <td>${escapeHTML(i.description)}</td>
              <td>
                <div style="font-size: 11px; color: var(--admin-accent); margin-bottom: 6px;">${escapeHTML(i.remediation)}</div>
                ${i.can_auto_harden ? `
                  <button class="admin-btn admin-btn-sm admin-btn-primary" onclick="hardenWorkload('${escapeHTML(i.namespace)}', '${escapeHTML(i.workload_name)}')">1-Click Harden</button>
                ` : ""}
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

async function hardenWorkload(namespace, deploymentName) {
  if (!confirm(`Apply CIS Workload Hardening to deployment "${deploymentName}" in namespace "${namespace}"? This sets runAsNonRoot: true, drops all capabilities, and sets allowPrivilegeEscalation: false.`)) {
    return;
  }
  try {
    const res = await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(deploymentName)}/harden`, {
      method: "POST",
    });
    alert(res.message || "Workload successfully hardened.");
    renderSecurityAudit($("#admin-content-area"));
  } catch (err) {
    alert("Hardening failed: " + err.message);
  }
}

async function renderRBAC(container) {
  const rbac = await request("/api/v1/admin/security/rbac");

  let riskColor = "green";
  let riskTitle = "Low Privilege Risk";
  if (rbac.risk_score >= 60) {
    riskColor = "red";
    riskTitle = "High Blast Radius Detected";
  } else if (rbac.risk_score >= 30) {
    riskColor = "amber";
    riskTitle = "Moderate Privilege Exposure";
  }

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>RBAC Blast Radius & Privilege Escalation Analyzer</h1></div>
      <button class="admin-btn admin-btn-primary" onclick="renderRBAC($('#admin-content-area'))">Re-scan RBAC</button>
    </div>

    <div class="admin-score-banner">
      <div class="admin-score-circle ${riskColor}">${rbac.risk_score}</div>
      <div class="admin-score-details">
        <h3>Cluster Privilege Risk: ${riskTitle}</h3>
        <p>${rbac.total_bindings_scanned} role bindings evaluated across cluster scopes.</p>
        <p style="margin-top: 6px; color: var(--admin-text-muted);">
          <strong>${rbac.cluster_admin_count}</strong> cluster-admin bindings ·
          <strong>${rbac.privilege_escalation_count}</strong> privilege escalation paths ·
          <strong>${rbac.wildcard_permissions_count}</strong> wildcard roles ·
          <strong>${rbac.cross_tenant_bindings_count}</strong> cross-tenant leaks
        </p>
      </div>
    </div>

    <div class="admin-overview-grid" style="margin-top: 20px;">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>cluster-admin</span><span>Superuser</span></div>
        <div class="admin-stat-value" style="color: ${rbac.cluster_admin_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${rbac.cluster_admin_count}</div>
        <div class="admin-stat-subtext">Bindings granting full cluster control</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Escalation Verbs</span><span>bind / escalate</span></div>
        <div class="admin-stat-value" style="color: ${rbac.privilege_escalation_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${rbac.privilege_escalation_count}</div>
        <div class="admin-stat-subtext">Ability to grant oneself permissions</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Wildcard Roles</span><span>* on *</span></div>
        <div class="admin-stat-value" style="color: ${rbac.wildcard_permissions_count > 0 ? "var(--admin-amber)" : "var(--admin-green)"};">${rbac.wildcard_permissions_count}</div>
        <div class="admin-stat-subtext">Broad wildcard verb/resource rules</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Cross-Tenant Leaks</span><span>Capsule Boundary</span></div>
        <div class="admin-stat-value" style="color: ${rbac.cross_tenant_bindings_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${rbac.cross_tenant_bindings_count}</div>
        <div class="admin-stat-subtext">Tenant SAs with external cluster access</div>
      </div>
    </div>

    <h2 style="font-size: 15px; margin: 24px 0 10px;">High-Risk RBAC Bindings</h2>
    <div class="admin-table-container" style="margin-bottom: 24px;">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Risk Level</th>
            <th>Binding Name</th>
            <th>Type</th>
            <th>Role Ref</th>
            <th>Subjects Bound</th>
            <th>Risk Analysis</th>
            <th>Remediation</th>
          </tr>
        </thead>
        <tbody>
          ${rbac.high_risk_bindings.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No high-risk bindings detected.</td></tr>` : ""}
          ${rbac.high_risk_bindings.map((b) => `
            <tr>
              <td><span class="admin-badge ${b.risk_level === "critical" ? "admin-badge-crashloop" : "admin-badge-warning"}">${escapeHTML(b.risk_level.toUpperCase())}</span></td>
              <td><strong>${escapeHTML(b.name)}</strong></td>
              <td><span class="admin-kind-badge">${escapeHTML(b.kind)}</span></td>
              <td><code>${escapeHTML(b.role_ref)}</code></td>
              <td>
                <div style="display: flex; flex-wrap: wrap; gap: 4px;">
                  ${b.subjects.map((s) => `<span class="admin-tag-chip">${escapeHTML(s)}</span>`).join("")}
                </div>
              </td>
              <td>
                <ul style="margin: 0; padding-left: 16px; font-size: 11px; color: var(--admin-red);">
                  ${b.risk_reasons.map((r) => `<li>${escapeHTML(r)}</li>`).join("")}
                </ul>
              </td>
              <td style="font-size: 11px; color: var(--admin-text-muted);">${escapeHTML(b.remediation)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>

    <h2 style="font-size: 15px; margin: 24px 0 10px;">Service Account Blast Radius</h2>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Service Account</th>
            <th>Namespace</th>
            <th>Tenant Space?</th>
            <th>Bound Roles</th>
            <th>Risk Exposure</th>
          </tr>
        </thead>
        <tbody>
          ${rbac.service_account_risks.length === 0 ? `<tr><td colspan="5" class="admin-empty-state">No elevated service accounts detected.</td></tr>` : ""}
          ${rbac.service_account_risks.map((sa) => `
            <tr>
              <td><strong>${escapeHTML(sa.service_account)}</strong></td>
              <td class="admin-table-ns">${escapeHTML(sa.namespace)}</td>
              <td>
                ${sa.is_tenant ? `<span class="admin-badge admin-badge-accent">Tenant Workspace</span>` : `<span class="admin-badge">System</span>`}
              </td>
              <td>
                ${sa.roles_bound.map((r) => `<code>${escapeHTML(r)}</code>`).join(" ")}
              </td>
              <td style="color: var(--admin-accent); font-size: 11px;">${escapeHTML(sa.risk_summary)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>
  `;
}

async function renderRightSizing(container) {
  const sizing = await request("/api/v1/admin/ai/rightsizing");

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>AI Resource Right-Sizing & Cost Advisor</h1></div>
      <button class="admin-btn admin-btn-primary" onclick="renderRightSizing($('#admin-content-area'))">Re-evaluate Resources</button>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Estimated Monthly Waste</span><span>Cloud Costs</span></div>
        <div class="admin-stat-value" style="color: ${sizing.estimated_monthly_savings_usd > 0 ? "var(--admin-accent)" : "var(--admin-green)"};">
          $${sizing.estimated_monthly_savings_usd.toFixed(2)}
        </div>
        <div class="admin-stat-subtext">Estimated potential savings / month</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Overprovisioned</span><span>High Allocation</span></div>
        <div class="admin-stat-value" style="color: ${sizing.overprovisioned_count > 0 ? "var(--admin-amber)" : "var(--admin-green)"};">
          ${sizing.overprovisioned_count}
        </div>
        <div class="admin-stat-subtext">Workloads exceeding baselines</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Uncapped Workloads</span><span>Missing Limits</span></div>
        <div class="admin-stat-value" style="color: ${sizing.missing_limits_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${sizing.missing_limits_count}</div>
        <div class="admin-stat-subtext">Containers at risk of eviction</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Evaluated Deployments</span><span>Total</span></div>
        <div class="admin-stat-value">${sizing.total_deployments}</div>
        <div class="admin-stat-subtext">Cluster waste rate: ${sizing.estimated_cluster_waste_percent}%</div>
      </div>
    </div>

    <h2 style="font-size: 15px; margin: 24px 0 10px;">Workload Right-Sizing Recommendations</h2>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Deployment</th>
            <th>Namespace</th>
            <th>Current (Req / Lim)</th>
            <th>Recommended Baseline</th>
            <th>Est. Savings</th>
            <th>Risk / Efficiency</th>
            <th>Reasoning</th>
            <th>Action</th>
          </tr>
        </thead>
        <tbody>
          ${sizing.recommendations.length === 0 ? `<tr><td colspan="8" class="admin-empty-state">No deployments found to analyze.</td></tr>` : ""}
          ${sizing.recommendations.map((r) => `
            <tr>
              <td class="admin-table-name" onclick="openInspector('deployment', '${escapeHTML(r.namespace)}', '${escapeHTML(r.deployment_name)}')">
                <strong>${escapeHTML(r.deployment_name)}</strong>
                <div style="font-size: 10px; color: var(--admin-text-muted);">${escapeHTML(r.container_name)}</div>
              </td>
              <td class="admin-table-ns">${escapeHTML(r.namespace)}</td>
              <td>
                <div style="font-size: 11px;">CPU: <code>${escapeHTML(r.current_cpu_request)}</code> / <code>${escapeHTML(r.current_cpu_limit)}</code></div>
                <div style="font-size: 11px;">RAM: <code>${escapeHTML(r.current_mem_request)}</code> / <code>${escapeHTML(r.current_mem_limit)}</code></div>
              </td>
              <td>
                <div style="font-size: 11px; color: var(--admin-green);">CPU: <strong>${escapeHTML(r.recommended_cpu_request)}</strong> / <strong>${escapeHTML(r.recommended_cpu_limit)}</strong></div>
                <div style="font-size: 11px; color: var(--admin-green);">RAM: <strong>${escapeHTML(r.recommended_mem_request)}</strong> / <strong>${escapeHTML(r.recommended_mem_limit)}</strong></div>
              </td>
              <td>
                ${r.monthly_savings_usd > 0 ? `<strong style="color: var(--admin-green);">+$${r.monthly_savings_usd.toFixed(2)}/mo</strong>` : "-"}
              </td>
              <td>
                <span class="admin-badge ${r.risk_level === "High Waste" ? "admin-badge-warning" : r.risk_level === "Uncapped" ? "admin-badge-crashloop" : "admin-badge-active"}">
                  ${escapeHTML(r.risk_level)}
                </span>
              </td>
              <td style="font-size: 11px; max-width: 240px;">${escapeHTML(r.reason)}</td>
              <td>
                <button class="admin-btn admin-btn-sm admin-btn-primary" onclick="openRightSizeModal('${escapeHTML(r.namespace)}', '${escapeHTML(r.deployment_name)}', '${escapeHTML(r.recommended_cpu_request)}', '${escapeHTML(r.recommended_cpu_limit)}', '${escapeHTML(r.recommended_mem_request)}', '${escapeHTML(r.recommended_mem_limit)}', ${r.monthly_savings_usd})">
                  Apply Sizing
                </button>
              </td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>
  `;
}

function openRightSizeModal(namespace, deploymentName, cpuReq, cpuLim, memReq, memLim, savings) {
  const modal = $("#admin-rightsize-modal");
  $("#rightsize-dep-name").textContent = deploymentName;
  $("#rightsize-dep-ns").textContent = namespace;
  $("#rightsize-cpu-req").value = cpuReq || "100m";
  $("#rightsize-cpu-lim").value = cpuLim || "500m";
  $("#rightsize-mem-req").value = memReq || "128Mi";
  $("#rightsize-mem-lim").value = memLim || "512Mi";
  $("#rightsize-savings-note").textContent = savings > 0 ? `Est. monthly savings: ~$${savings.toFixed(2)}/month per replica.` : "";
  modal.dataset.namespace = namespace;
  modal.dataset.deployment = deploymentName;
  modal.classList.add("open");
}

async function renderCopilot(container) {
  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>Natural Language AI K8s Copilot</h1></div>
    </div>

    <div class="admin-copilot-container">
      <div class="admin-copilot-hero">
        <div class="admin-copilot-hero-title">
          <span class="admin-copilot-sparkle">AI</span>
          <h2>Text-to-K8s Cluster Intelligence</h2>
        </div>
        <p>Ask natural questions to inspect crashing workloads, evaluate security posture, analyze RBAC blast radius, or find cost savings.</p>

        <form id="copilot-main-form" style="margin-top: 16px;">
          <div style="display: flex; gap: 8px;">
            <input class="text-input" id="copilot-main-input" placeholder="e.g. 'Which pods are crashing?', 'Check secret leaks', 'Show cluster-admin bindings'..." style="flex: 1; font-size: 14px; padding: 10px 14px;" autocomplete="off">
            <button class="admin-btn admin-btn-primary" type="submit" style="padding: 0 20px;">Ask Copilot</button>
          </div>
        </form>

        <div class="admin-quick-prompts" style="margin-top: 12px;">
          <button class="admin-quick-cmd-btn" type="button" data-prompt="Which pods are crashing or restarting?">Crashing pods</button>
          <button class="admin-quick-cmd-btn" type="button" data-prompt="Show me security vulnerabilities and root containers">Security scan</button>
          <button class="admin-quick-cmd-btn" type="button" data-prompt="Check cluster-admin bindings and RBAC blast radius">RBAC blast radius</button>
          <button class="admin-quick-cmd-btn" type="button" data-prompt="Find overprovisioned workloads and right-size recommendations">Cost right-sizing</button>
          <button class="admin-quick-cmd-btn" type="button" data-prompt="Show all warning events across the cluster">Warning events</button>
          <button class="admin-quick-cmd-btn" type="button" data-prompt="Are there any plaintext secret leaks in env or ConfigMaps?">Secret leaks</button>
        </div>
      </div>

      <div id="copilot-main-results" style="margin-top: 24px;"></div>
    </div>
  `;

  const form = $("#copilot-main-form");
  const input = $("#copilot-main-input");
  const results = $("#copilot-main-results");

  async function executeCopilotQuery(q) {
    if (!q || !q.trim()) return;
    input.value = q;
    results.innerHTML = `<div class="admin-empty-state">Evaluating query against cluster state…</div>`;

    try {
      const resp = await request("/api/v1/admin/ai/copilot", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ query: q }),
      });

      let matchesHtml = "";
      if (resp.matches && resp.matches.length > 0) {
        matchesHtml = `
          <h3 style="font-size: 14px; margin: 20px 0 10px;">Matching Resources (${resp.matches.length})</h3>
          <div class="admin-table-container">
            <table class="admin-table">
              <thead><tr><th>Kind</th><th>Name</th><th>Namespace</th><th>Status</th><th>Details</th><th>Action</th></tr></thead>
              <tbody>
                ${resp.matches.map((m) => `
                  <tr>
                    <td><span class="admin-kind-badge">${escapeHTML(m.kind)}</span></td>
                    <td class="admin-table-name"><strong>${escapeHTML(m.name)}</strong></td>
                    <td class="admin-table-ns">${escapeHTML(m.namespace || "-")}</td>
                    <td><span class="admin-badge ${m.status === "CrashLoopBackOff" || m.status === "critical" ? "admin-badge-crashloop" : "admin-badge-warning"}">${escapeHTML(m.status)}</span></td>
                    <td style="font-size: 11px;">${escapeHTML(m.reason ? `${m.reason}: ` : "")}${escapeHTML(m.message || "")}</td>
                    <td>
                      ${m.link ? `<a class="admin-btn admin-btn-sm" href="${escapeHTML(m.link)}">Inspect</a>` : ""}
                    </td>
                  </tr>
                `).join("")}
              </tbody>
            </table>
          </div>
        `;
      }

      let actionsHtml = "";
      if (resp.suggested_actions && resp.suggested_actions.length > 0) {
        actionsHtml = `
          <div style="margin-top: 20px;">
            <h3 style="font-size: 14px; margin-bottom: 10px;">Suggested Actions</h3>
            <div style="display: flex; gap: 10px; flex-wrap: wrap;">
              ${resp.suggested_actions.map((act) => `
                <div class="admin-remediation-card" style="flex: 1; min-width: 260px;">
                  <div class="admin-remediation-info">
                    <strong>${escapeHTML(act.title)}</strong>
                    <span>${escapeHTML(act.description)}</span>
                  </div>
                  ${act.route ? `<a class="admin-btn admin-btn-primary admin-btn-sm" href="${escapeHTML(act.route)}">Execute</a>` : ""}
                </div>
              `).join("")}
            </div>
          </div>
        `;
      }

      results.innerHTML = `
        <div class="admin-ai-banner">
          <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;">
            <span class="admin-tag-chip" style="color: var(--admin-accent); font-weight: 700;">Intent: ${escapeHTML(resp.intent)}</span>
            <span style="font-size: 11px; color: var(--admin-text-muted);">Query: "${escapeHTML(resp.query)}"</span>
          </div>
          <p style="margin: 0; font-size: 13px; line-height: 1.6; color: var(--admin-text);">${escapeHTML(resp.summary)}</p>
        </div>
        ${actionsHtml}
        ${matchesHtml}
      `;
    } catch (err) {
      results.innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Query failed: ${escapeHTML(err.message)}</div>`;
    }
  }

  form.addEventListener("submit", (e) => {
    e.preventDefault();
    executeCopilotQuery(input.value);
  });

  $$(".admin-quick-prompts button").forEach((btn) => {
    btn.addEventListener("click", () => {
      executeCopilotQuery(btn.dataset.prompt);
    });
  });

  executeCopilotQuery("Which pods are crashing or restarting?");
}

async function renderDatabaseTopology(container) {
  const topo = await request("/api/v1/admin/databases/topology");

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>Managed Database & Operator Topology</h1></div>
      <button class="admin-btn admin-btn-primary" onclick="renderDatabaseTopology($('#admin-content-area'))">Refresh</button>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>PostgreSQL Clusters</span><span>CloudNativePG</span></div>
        <div class="admin-stat-value">${topo.postgres_clusters ? topo.postgres_clusters.length : 0}</div>
        <div class="admin-stat-subtext">3-node HA with streaming replication</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>MySQL Clusters</span><span>InnoDB</span></div>
        <div class="admin-stat-value">${topo.mysql_clusters ? topo.mysql_clusters.length : 0}</div>
        <div class="admin-stat-subtext">3-instance quorum cluster</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Storage Leaks</span><span>Orphan Claims</span></div>
        <div class="admin-stat-value" style="color: ${topo.storage_leaks && topo.storage_leaks.length > 0 ? "var(--admin-amber)" : "var(--admin-green)"};">
          ${topo.storage_leaks ? topo.storage_leaks.length : 0}
        </div>
        <div class="admin-stat-subtext">Unmounted tenant claims</div>
      </div>
    </div>

    <h2 style="font-size: 15px; margin: 20px 0 10px;">PostgreSQL Instances (CloudNativePG)</h2>
    <div class="admin-table-container" style="margin-bottom: 24px;">
      <table class="admin-table">
        <thead><tr><th>Cluster Name</th><th>Namespace</th><th>Status</th><th>Primary (Read/Write)</th><th>Standbys</th><th>Backup State</th><th>Age</th></tr></thead>
        <tbody>
          ${!topo.postgres_clusters || topo.postgres_clusters.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No CloudNativePG database clusters provisioned yet.</td></tr>` : ""}
          ${(topo.postgres_clusters || []).map((c) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(c.name)}</td>
              <td class="admin-table-ns">${escapeHTML(c.namespace)}</td>
              <td><span class="admin-badge admin-badge-ready"><span class="admin-badge-dot"></span>${escapeHTML(c.status)}</span></td>
              <td><strong>${escapeHTML(c.primary || `${c.name}-1`)}</strong></td>
              <td class="admin-table-ns">${escapeHTML((c.replicas || []).join(", "))}</td>
              <td><span class="admin-badge admin-badge-completed">WAL Active</span></td>
              <td class="admin-table-age">${escapeHTML(c.age)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>

    <h2 style="font-size: 15px; margin: 20px 0 10px;">MySQL InnoDB Clusters</h2>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Cluster Name</th><th>Namespace</th><th>Status</th><th>Primary (R/W)</th><th>Replicas</th><th>Backup State</th><th>Age</th></tr></thead>
        <tbody>
          ${!topo.mysql_clusters || topo.mysql_clusters.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No MySQL InnoDB clusters provisioned yet.</td></tr>` : ""}
          ${(topo.mysql_clusters || []).map((m) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(m.name)}</td>
              <td class="admin-table-ns">${escapeHTML(m.namespace)}</td>
              <td><span class="admin-badge admin-badge-ready"><span class="admin-badge-dot"></span>${escapeHTML(m.status)}</span></td>
              <td><strong>${escapeHTML(m.primary || `${m.name}-0`)}</strong></td>
              <td class="admin-table-ns">${escapeHTML((m.replicas || []).join(", "))}</td>
              <td><span class="admin-badge admin-badge-completed">Schedule Active</span></td>
              <td class="admin-table-age">${escapeHTML(m.age)}</td>
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
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Storage Leak Detector</h1>
        <span class="admin-view-count">${leaks.length}</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-primary" onclick="renderStorageLeaks($('#admin-content-area'))">Scan for Leaks</button>
      </div>
    </div>
    <p style="color: var(--admin-text-muted); font-size: 12px; margin-bottom: 16px;">
      Persistent Volume Claims (PVCs) created by customer apps that are currently unmounted and retained after workload deletion. Purging frees underlying cloud storage disk resources.
    </p>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Claim Name</th><th>Namespace</th><th>Capacity</th><th>StorageClass</th><th>Reason</th><th>Age</th><th style="text-align: right;">Purge</th></tr></thead>
        <tbody>
          ${leaks.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">Clean cluster. No orphaned persistent volume claims detected.</td></tr>` : ""}
          ${leaks.map((item) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(item.pvc_name)}</td>
              <td class="admin-table-ns">${escapeHTML(item.namespace)}</td>
              <td><strong>${escapeHTML(item.capacity || "5Gi")}</strong></td>
              <td>${escapeHTML(item.storage_class || "standard")}</td>
              <td style="color: var(--admin-amber); font-size: 11px;">${escapeHTML(item.reason)}</td>
              <td class="admin-table-age">${escapeHTML(item.age)}</td>
              <td style="text-align: right;">
                <button class="admin-btn admin-btn-danger admin-btn-sm" onclick="deletePVC('${escapeHTML(item.namespace)}', '${escapeHTML(item.pvc_name)}')">Purge Volume</button>
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
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>Cluster Resource Efficiency & Heatmap</h1></div>
      <button class="admin-btn admin-btn-primary" onclick="renderEfficiency($('#admin-content-area'))">Recalculate</button>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>CPU Requested</span><span>Total</span></div>
        <div class="admin-stat-value">${escapeHTML(eff.cpu_requests_total)}</div>
        <div class="admin-progress-wrap">
          <div class="admin-progress-bar">
            <div class="admin-progress-fill ${eff.cpu_utilization_percent > 80 ? "red" : "green"}" style="width: ${Math.min(eff.cpu_utilization_percent, 100)}%;"></div>
          </div>
        </div>
        <div class="admin-stat-subtext">${eff.cpu_utilization_percent.toFixed(1)}% of ${escapeHTML(eff.total_cpu_allocatable)} allocatable</div>
      </div>

      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Memory Requested</span><span>Total</span></div>
        <div class="admin-stat-value">${escapeHTML(eff.mem_requests_total)}</div>
        <div class="admin-progress-wrap">
          <div class="admin-progress-bar">
            <div class="admin-progress-fill ${eff.mem_utilization_percent > 80 ? "red" : "green"}" style="width: ${Math.min(eff.mem_utilization_percent, 100)}%;"></div>
          </div>
        </div>
        <div class="admin-stat-subtext">${eff.mem_utilization_percent.toFixed(1)}% of ${escapeHTML(eff.total_mem_allocatable)} allocatable</div>
      </div>
    </div>

    <h2 style="font-size: 15px; margin: 20px 0 10px;">Workspace Allocation Breakdown</h2>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Workspace</th><th>Namespace</th><th>Pods</th><th>CPU Request</th><th>CPU Limit</th><th>Mem Request</th><th>Mem Limit</th><th>Efficiency</th></tr></thead>
        <tbody>
          ${(eff.workspaces || []).length === 0 ? `<tr><td colspan="8" class="admin-empty-state">No active workspace workloads found.</td></tr>` : ""}
          ${(eff.workspaces || []).map((w) => `
            <tr>
              <td class="admin-table-name"><span class="admin-ws-tag">${escapeHTML(w.workspace_name)}</span></td>
              <td class="admin-table-ns">${escapeHTML(w.namespace)}</td>
              <td>${w.pods_count}</td>
              <td>${escapeHTML(w.cpu_request)}</td>
              <td>${escapeHTML(w.cpu_limit)}</td>
              <td>${escapeHTML(w.mem_request)}</td>
              <td>${escapeHTML(w.mem_limit)}</td>
              <td><span class="admin-badge ${w.efficiency === "Optimal" ? "admin-badge-ready" : "admin-badge-warning"}">${escapeHTML(w.efficiency)}</span></td>
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
    <div class="admin-view-header"><div class="admin-view-title"><h1>Connect Deployment Cluster</h1></div></div>
    <div style="max-width: 760px; background: var(--admin-surface); border: 1px solid var(--admin-border); border-radius: 9px; padding: 24px; box-shadow: var(--admin-shadow-sm);">
      <p style="color: var(--admin-text-muted); font-size: 13px; margin-top: 0; line-height: 1.6;">
        Connect the Kubernetes cluster that customer workspaces deploy to. Paste a self-contained kubeconfig with certificate data embedded.
      </p>
      <form id="admin-connect-form">
        <label class="form-label" for="cluster-kubeconfig">Kubeconfig YAML</label>
        <textarea class="text-input" id="cluster-kubeconfig" rows="10" placeholder="apiVersion: v1&#10;clusters:&#10;  - cluster:&#10;      server: https://127.0.0.1:6443&#10;..." required style="width: 100%; font-family: var(--admin-mono); font-size: 11px;"></textarea>
        
        <label class="form-label" for="cluster-context" style="margin-top: 14px;">Context (optional, defaults to current-context)</label>
        <input class="text-input" id="cluster-context" placeholder="e.g. minikube, kind-crelan, arn:aws:eks:..." style="width: 100%;">
        
        <div id="cluster-connect-state" style="margin: 14px 0;"></div>
        <button class="admin-btn admin-btn-primary" id="cluster-connect-submit" type="submit" style="padding: 9px 18px; font-size: 12px;">Validate and Connect Cluster</button>
      </form>
    </div>`;

  $("#admin-connect-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const btn = $("#cluster-connect-submit");
    const st = $("#cluster-connect-state");
    btn.disabled = true;
    st.innerHTML = `<p style="color: var(--admin-accent); font-weight: 600; font-size: 12px; margin: 8px 0;">Connecting to Kubernetes API server and verifying access…</p>`;
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), 15000);
    try {
      const res = await fetch("/api/v1/admin/clusters/connect", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        signal: controller.signal,
        body: JSON.stringify({
          kubeconfig: $("#cluster-kubeconfig").value,
          context: $("#cluster-context").value.trim(),
        }),
      });
      clearTimeout(timeoutId);
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || ("Connection failed (" + res.status + ")"));
      const warningHtml = data.persist_warning
        ? `<p style="font-size: 11px; margin: 6px 0 0; color: var(--admin-amber);">${escapeHTML(data.persist_warning)}</p>`
        : "";
      st.innerHTML = `
        <div style="padding: 14px 16px; background: var(--admin-green-bg); border: 1px solid var(--admin-green-border); border-radius: 6px; color: var(--admin-green); margin: 12px 0;">
          <strong style="display: block; font-size: 13px; margin-bottom: 2px;">Connected successfully to “${escapeHTML(data.context)}”!</strong>
          <span style="font-size: 12px;">The cluster is active and ready for platform administration and workloads.</span>
          ${warningHtml}
        </div>
        <div style="display: flex; gap: 8px; margin-top: 12px;">
          <a class="admin-btn admin-btn-primary" href="#pods">View Pods</a>
          <a class="admin-btn" href="#nodes">View Nodes</a>
          <a class="admin-btn" href="#overview">Go to Overview</a>
        </div>
      `;
      await loadClusterHeader();
      await loadNamespacesDropdown();
    } catch (err) {
      clearTimeout(timeoutId);
      const isTimeout = err.name === "AbortError" || (err.message && err.message.toLowerCase().includes("timeout"));
      const errMsg = isTimeout
        ? "Connection attempt timed out after 15 seconds. Ensure the Kubernetes API server URL is reachable from this server and any network/firewall allows access."
        : err.message;
      st.innerHTML = `
        <div style="padding: 14px 16px; background: var(--admin-red-bg); border: 1px solid var(--admin-red-border); border-radius: 6px; color: var(--admin-red); margin: 12px 0;">
          <strong style="display: block; font-size: 13px; margin-bottom: 2px;">Connection Failed</strong>
          <span style="font-size: 12px;">${escapeHTML(errMsg)}</span>
        </div>
      `;
    } finally {
      clearTimeout(timeoutId);
      btn.disabled = false;
    }
  });
}

async function renderReadiness(container) {
  const readiness = await request("/api/v1/admin/readiness");

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title"><h1>Cluster Readiness Checklist</h1></div>
      <button class="admin-btn admin-btn-primary" onclick="renderReadiness($('#admin-content-area'))">Re-check</button>
    </div>
    <div style="margin-bottom: 16px; padding: 12px 16px; border-radius: 6px; background: ${readiness.ready ? "var(--admin-green-bg)" : "var(--admin-amber-bg)"}; border: 1px solid ${readiness.ready ? "var(--admin-green-border)" : "var(--admin-amber-border)"};">
      <strong style="color: ${readiness.ready ? "var(--admin-green)" : "var(--admin-amber)"};">
        ${readiness.ready ? "All core cluster prerequisites verified." : "Some cluster components need attention before offering all managed database engines."}
      </strong>
    </div>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead><tr><th>Check Name</th><th>Status</th><th>Details</th></tr></thead>
        <tbody>
          ${readiness.checks.map((c) => `
            <tr>
              <td class="admin-table-name">${escapeHTML(c.name)}</td>
              <td><span class="admin-badge ${c.ready ? "admin-badge-ready" : "admin-badge-warning"}">${c.ready ? "Ready" : "Needs Attention"}</span></td>
              <td style="color: var(--admin-text-muted); font-size: 12px;">${escapeHTML(c.detail)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>`;
}

function renderBackup(container) {
  container.innerHTML = `
    <div class="admin-view-header"><div class="admin-view-title"><h1>Control-Plane Backup</h1></div></div>
    <div style="max-width: 600px; background: var(--admin-surface); border: 1px solid var(--admin-border); border-radius: 9px; padding: 24px;">
      <h3 style="margin-top: 0; font-family: var(--admin-font-heading);">SQLite Control-Plane Database Snapshot</h3>
      <p style="color: var(--admin-text-muted); font-size: 12px;">
        Download a consistent point-in-time snapshot of the Crelan control-plane SQLite database (including workspaces, deployments, users, and audit records).
      </p>
      <button class="admin-btn admin-btn-primary" id="btn-do-backup" type="button" style="margin-top: 12px;">
        Download Control-Plane Snapshot (.db)
      </button>
      <p id="backup-state-text" style="color: var(--admin-text-dim); font-size: 11px; margin-top: 10px;"></p>
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

  const toolbar = $("#drawer-toolbar");
  toolbar.replaceChildren();

  if (kind === "pod") {
    const btnDiag = createBtn("Diagnose", "", () => openAIDiagnosis(namespace, name));
    const btnLogs = createBtn("Logs", "", () => openPodLogs(namespace, name));
    const btnExec = createBtn("Terminal", "", () => openPodTerminal(namespace, name));
    const btnDebug = createBtn("Debug Shell", "", () => openEphemeralDebugModal(namespace, name));
    const btnDel = createBtn("Delete", "admin-btn-danger", () => confirmDeletePod(namespace, name));
    toolbar.append(btnDiag, btnLogs, btnExec, btnDebug, btnDel);
  } else if (kind === "deployment") {
    const btnRestart = createBtn("Rollout Restart", "", () => triggerRolloutRestart(namespace, name));
    const btnScale = createBtn("Scale", "", () => openScaleModal(namespace, name, 1));
    const btnDel = createBtn("Delete", "admin-btn-danger", () => confirmDeleteDeployment(namespace, name));
    toolbar.append(btnRestart, btnScale, btnDel);
  }

  $$(".admin-drawer-tab").forEach((t) => t.classList.toggle("active", t.dataset.tab === "overview"));

  $("#admin-drawer-overlay").classList.add("open");
  $("#admin-inspector-drawer").classList.add("open");

  await renderInspectorTab("overview");
}

function createBtn(text, extraClass, handler) {
  const b = document.createElement("button");
  b.className = `admin-btn admin-btn-sm ${extraClass}`;
  b.textContent = text;
  b.onclick = handler;
  return b;
}

function closeInspector() {
  $("#admin-drawer-overlay").classList.remove("open");
  $("#admin-inspector-drawer").classList.remove("open");
  state.activeInspector = null;
}

async function renderInspectorTab(tab) {
  if (!state.activeInspector) return;
  const { kind, namespace, name } = state.activeInspector;
  const body = $("#drawer-body");
  body.innerHTML = `<div class="admin-empty-state">Loading ${tab}…</div>`;

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
        <div class="admin-table-container">
          <table class="admin-table">
            <thead><tr><th>Type</th><th>Reason</th><th>Message</th><th>Age</th></tr></thead>
            <tbody>
              ${scoped.length === 0 ? `<tr><td colspan="4" class="admin-empty-state">No events recorded for this resource.</td></tr>` : ""}
              ${scoped.map((e) => `
                <tr>
                  <td><span class="admin-badge ${e.type === "Warning" ? "admin-badge-warning" : "admin-badge-ready"}">${escapeHTML(e.type)}</span></td>
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
        <div class="admin-yaml-wrap">
          <button class="admin-yaml-copy-btn" id="btn-copy-yaml">Copy JSON</button>
          <pre class="admin-yaml-code">${escapeHTML(jsonStr)}</pre>
        </div>`;
      $("#btn-copy-yaml").onclick = () => {
        navigator.clipboard.writeText(jsonStr);
        $("#btn-copy-yaml").textContent = "Copied!";
        setTimeout(() => ($("#btn-copy-yaml").textContent = "Copy JSON"), 2000);
      };
    }
  } catch (err) {
    body.innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Error: ${escapeHTML(err.message)}</div>`;
  }
}

function renderInspectorOverview(container, kind, raw) {
  const meta = raw.metadata || {};
  let specDetails = "";

  if (kind === "secret" && raw.data) {
    specDetails = `
      <div class="admin-spec-group">
        <div class="admin-spec-group-title">Decoded Secret Data (${raw.keys} keys)</div>
        ${Object.entries(raw.data).map(([k, v]) => `
          <div class="admin-spec-row">
            <span class="admin-spec-label">${escapeHTML(k)}</span>
            <span class="admin-spec-val" style="color: var(--admin-amber);">${escapeHTML(v)}</span>
          </div>
        `).join("")}
      </div>`;
  }

  container.innerHTML = `
    <div class="admin-spec-group">
      <div class="admin-spec-group-title">Resource Metadata</div>
      <div class="admin-spec-row"><span class="admin-spec-label">Name</span><span class="admin-spec-val">${escapeHTML(meta.name || raw.name)}</span></div>
      <div class="admin-spec-row"><span class="admin-spec-label">Namespace</span><span class="admin-spec-val">${escapeHTML(meta.namespace || raw.namespace || "-")}</span></div>
      <div class="admin-spec-row"><span class="admin-spec-label">UID</span><span class="admin-spec-val">${escapeHTML(meta.uid || "-")}</span></div>
      <div class="admin-spec-row"><span class="admin-spec-label">Created</span><span class="admin-spec-val">${escapeHTML(meta.creationTimestamp || raw.created_at || "-")}</span></div>
    </div>
    ${specDetails}
    <div class="admin-spec-group">
      <div class="admin-spec-group-title">Labels</div>
      ${meta.labels ? Object.entries(meta.labels).map(([k, v]) => `
        <div class="admin-spec-row"><span class="admin-spec-label">${escapeHTML(k)}</span><span class="admin-spec-val">${escapeHTML(v)}</span></div>
      `).join("") : `<p style="color: var(--admin-text-dim); font-size: 11px;">None</p>`}
    </div>
  `;
}

// -------------------------------------------------------------
// Interactive In-Browser Pod Exec
// -------------------------------------------------------------

function openPodTerminal(namespace, podName, container = "") {
  state.activeTerminal = { pod: podName, namespace, container };
  $("#terminal-pod-name").textContent = podName;
  $("#terminal-container-name").textContent = container || "default";
  $("#terminal-prompt").textContent = `[${escapeHTML(podName)}]$`;
  $("#admin-terminal-drawer").classList.add("open");
  $("#terminal-input").focus();
}

function closePodTerminal() {
  $("#admin-terminal-drawer").classList.remove("open");
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
// Pod Logs Viewer
// -------------------------------------------------------------

async function openPodLogs(namespace, podName) {
  state.activeLogs = { pod: podName, namespace, container: "" };
  $("#logs-pod-name").textContent = `${namespace}/${podName}`;
  $("#admin-logs-drawer").classList.add("open");

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
  $("#admin-logs-drawer").classList.remove("open");
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
  const modal = $("#admin-ai-modal");
  const body = $("#ai-modal-body");
  modal.classList.add("open");
  body.innerHTML = `<div class="admin-empty-state">Running root-cause diagnosis for ${escapeHTML(podName)}…</div>`;

  try {
    const diag = await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}/diagnose`);

    let exitCodeHtml = "";
    if (diag.exit_code !== undefined && diag.exit_code !== 0) {
      exitCodeHtml = `<span class="admin-badge admin-badge-crashloop">Exit Code ${escapeHTML(diag.exit_code)}${diag.last_termination_reason ? ` (${escapeHTML(diag.last_termination_reason)})` : ""}</span>`;
    }

    body.innerHTML = `
      <div class="admin-ai-banner">
        <div style="display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 6px;">
          <div class="admin-ai-banner-title" style="margin: 0;">
            ${escapeHTML(diag.summary)}
          </div>
          ${exitCodeHtml}
        </div>
        <div style="font-size: 11px; color: var(--admin-text-muted); margin-bottom: 8px;">Confidence: <strong>${escapeHTML(diag.confidence)}</strong> · Severity: <strong>${escapeHTML(diag.severity.toUpperCase())}</strong></div>
        <div style="display: flex; gap: 8px; margin-top: 8px;">
          <button class="admin-btn admin-btn-primary admin-btn-sm" type="button" onclick="$('#admin-ai-modal').classList.remove('open'); openPostMortem('${escapeHTML(namespace)}', '${escapeHTML(podName)}')">Generate Post-Mortem & Runbook</button>
          <a class="admin-btn admin-btn-sm" href="#war-room?ns=${encodeURIComponent(namespace)}&pod=${encodeURIComponent(podName)}" onclick="$('#admin-ai-modal').classList.remove('open')">Incident War Room</a>
        </div>
      </div>

      ${diag.log_anomaly ? `
        <div class="admin-ai-anomaly-box" style="background: rgba(220, 38, 38, 0.08); border-left: 3px solid var(--admin-red); padding: 10px 14px; margin-bottom: 14px; border-radius: 4px;">
          <div style="font-size: 11px; font-weight: 700; text-transform: uppercase; color: var(--admin-red); margin-bottom: 4px;">Detected Log Anomaly / Error</div>
          <div style="font-family: monospace; font-size: 12px; color: var(--admin-red); word-break: break-all;">${escapeHTML(diag.log_anomaly)}</div>
        </div>
      ` : ""}

      <div class="admin-ai-cause-box">
        <div class="admin-ai-cause-title">Probable Cause & Root Analysis</div>
        <p style="margin: 0; font-size: 12px; line-height: 1.6; color: var(--admin-text);">${escapeHTML(diag.probable_cause)}</p>
      </div>

      ${diag.stack_trace ? `
        <div class="admin-spec-group">
          <div class="admin-spec-group-title" style="display: flex; justify-content: space-between; align-items: center;">
            <span>Extracted Application Stack Trace</span>
            <span style="font-size: 10px; font-weight: normal; color: var(--admin-text-muted);">Automated RCA</span>
          </div>
          <pre class="admin-yaml-code" style="max-height: 220px; color: #dc2626; background: #fff5f5; border: 1px solid #fecaca;">${escapeHTML(diag.stack_trace)}</pre>
        </div>
      ` : ""}

      ${diag.recommended_steps && diag.recommended_steps.length > 0 ? `
        <div class="admin-spec-group">
          <div class="admin-spec-group-title">Recommended Action Plan</div>
          <ol style="margin: 0; padding-left: 20px; font-size: 12px; color: var(--admin-text); line-height: 1.7;">
            ${diag.recommended_steps.map((st) => `<li>${escapeHTML(st)}</li>`).join("")}
          </ol>
        </div>
      ` : ""}

      <div class="admin-spec-group">
        <div class="admin-spec-group-title">Diagnostic Evidence</div>
        <ul style="margin: 0; padding-left: 20px; font-size: 12px; color: var(--admin-text-muted);">
          ${diag.evidence.map((ev) => `<li style="margin-bottom: 4px;">${escapeHTML(ev)}</li>`).join("")}
        </ul>
      </div>

      <div class="admin-spec-group">
        <div class="admin-spec-group-title">Remediation Actions</div>
        ${diag.remediation.map((act) => `
          <div class="admin-remediation-card">
            <div class="admin-remediation-info">
              <strong>${escapeHTML(act.title)}</strong>
              <span>${escapeHTML(act.description)}</span>
            </div>
            ${act.auto_fixable ? `<button class="admin-btn admin-btn-primary admin-btn-sm" onclick="runAutoFix('${escapeHTML(act.action_id)}', '${escapeHTML(namespace)}', '${escapeHTML(podName)}')">Apply Fix</button>` : ""}
          </div>
        `).join("")}
      </div>

      ${diag.relevant_logs ? `
        <div class="admin-spec-group">
          <div class="admin-spec-group-title">Recent Crash Logs</div>
          <pre class="admin-yaml-code" style="max-height: 200px;">${escapeHTML(diag.relevant_logs)}</pre>
        </div>` : ""}
    `;
  } catch (err) {
    body.innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Diagnosis failed: ${escapeHTML(err.message)}</div>`;
  }
}

async function runAutoFix(actionId, namespace, podName) {
  try {
    if (actionId === "restart_pod") {
      await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}?force=true`, { method: "DELETE" });
      alert(`Pod ${podName} evicted. Controller is restarting container.`);
    } else if (actionId === "restart_deployment") {
      const parts = podName.split("-");
      parts.pop();
      parts.pop();
      const depName = parts.join("-");
      await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(depName)}/restart`, { method: "POST" });
      alert(`Rollout restart triggered for deployment ${depName}.`);
    }
    $("#admin-ai-modal").classList.remove("open");
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
  $("#admin-scale-modal").classList.add("open");
}

function openCreateNamespaceModal() {
  $("#new-ns-name").value = "";
  $("#admin-create-ns-modal").classList.add("open");
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

function shouldAutoRefresh() {
  if (state.activeTerminal || state.activeLogs || state.activeInspector) return false;
  if (state.activeView === "cluster-connect" || state.activeView === "backup") return false;
  const tag = (document.activeElement && document.activeElement.tagName) || "";
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return false;
  if (document.querySelector(".admin-modal-overlay.open")) return false;
  return true;
}

function setupEventListeners() {
  window.addEventListener("hashchange", renderCurrentView);

  // Topbar Namespace Select
  $("#admin-namespace-select").addEventListener("change", (e) => {
    state.selectedNamespace = e.target.value;
    renderCurrentView();
  });

  // Topbar Search Filter
  $("#admin-search-input").addEventListener("input", (e) => {
    state.searchQuery = e.target.value.trim();
    renderCurrentView();
  });

  // Hotkey "/" for search
  window.addEventListener("keydown", (e) => {
    if (e.key === "/" && document.activeElement.tagName !== "INPUT" && document.activeElement.tagName !== "TEXTAREA") {
      e.preventDefault();
      $("#admin-search-input").focus();
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
  $("#admin-refresh-btn").addEventListener("click", async () => {
    const icon = $("#admin-refresh-icon");
    icon.style.display = "inline-block";
    icon.animate([{ opacity: 0.4 }, { opacity: 1 }], { duration: 400 });
    await Promise.all([loadClusterHeader(), renderCurrentView()]);
  });

  $("#rail-refresh-btn").addEventListener("click", () => $("#admin-refresh-btn").click());

  // Auto-Refresh
  $("#admin-auto-refresh-select").addEventListener("change", (e) => {
    if (state.autoRefreshTimer) clearInterval(state.autoRefreshTimer);
    const ms = parseInt(e.target.value, 10);
    if (ms > 0) {
      state.autoRefreshTimer = setInterval(() => {
        if (shouldAutoRefresh()) {
          loadClusterHeader();
          renderCurrentView();
        }
      }, ms);
    }
  });

  // Drawer Tabs & Close
  $("#drawer-close-btn").addEventListener("click", closeInspector);
  $("#admin-drawer-overlay").addEventListener("click", closeInspector);
  $$(".admin-drawer-tab").forEach((btn) => {
    btn.addEventListener("click", () => {
      $$(".admin-drawer-tab").forEach((t) => t.classList.remove("active"));
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
  $$(".admin-quick-cmd-btn").forEach((btn) => {
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
  $("#ai-modal-close").addEventListener("click", () => $("#admin-ai-modal").classList.remove("open"));
  $("#ai-modal-dismiss").addEventListener("click", () => $("#admin-ai-modal").classList.remove("open"));

  // Scale Modal Range Sync
  $("#scale-range").addEventListener("input", (e) => ($("#scale-input").value = e.target.value));
  $("#scale-input").addEventListener("input", (e) => ($("#scale-range").value = e.target.value));
  $("#scale-modal-cancel").addEventListener("click", () => $("#admin-scale-modal").classList.remove("open"));
  $("#scale-modal-close").addEventListener("click", () => $("#admin-scale-modal").classList.remove("open"));
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
      $("#admin-scale-modal").classList.remove("open");
      renderCurrentView();
    } catch (err) {
      alert("Scale failed: " + err.message);
    }
  });

  // Create Namespace Modal
  $("#create-ns-close").addEventListener("click", () => $("#admin-create-ns-modal").classList.remove("open"));
  $("#create-ns-cancel").addEventListener("click", () => $("#admin-create-ns-modal").classList.remove("open"));
  $("#create-ns-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const name = $("#new-ns-name").value.trim();
    try {
      await request("/api/v1/admin/namespaces", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      });
      $("#admin-create-ns-modal").classList.remove("open");
      await loadNamespacesDropdown();
      renderCurrentView();
    } catch (err) {
      alert("Namespace creation failed: " + err.message);
    }
  });

  // Right-Sizing Modal
  $("#rightsize-modal-close").addEventListener("click", () => $("#admin-rightsize-modal").classList.remove("open"));
  $("#rightsize-modal-cancel").addEventListener("click", () => $("#admin-rightsize-modal").classList.remove("open"));
  $("#rightsize-modal-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const modal = $("#admin-rightsize-modal");
    const ns = modal.dataset.namespace;
    const name = modal.dataset.deployment;
    const cpuReq = $("#rightsize-cpu-req").value.trim();
    const cpuLim = $("#rightsize-cpu-lim").value.trim();
    const memReq = $("#rightsize-mem-req").value.trim();
    const memLim = $("#rightsize-mem-lim").value.trim();
    try {
      const res = await request(`/api/v1/admin/deployments/${encodeURIComponent(ns)}/${encodeURIComponent(name)}/rightsize`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          cpu_request: cpuReq,
          cpu_limit: cpuLim,
          mem_request: memReq,
          mem_limit: memLim,
        }),
      });
      modal.classList.remove("open");
      alert(res.message || "Resources successfully updated.");
      if (state.activeView === "rightsizing") {
        renderRightSizing($("#admin-content-area"));
      } else {
        renderCurrentView();
      }
    } catch (err) {
      alert("Right-sizing failed: " + err.message);
    }
  });

  // Copilot Modal & Keyboard Shortcut
  function openQuickCopilot(prompt = "") {
    const modal = $("#admin-copilot-modal");
    modal.classList.add("open");
    const inp = $("#copilot-modal-input");
    inp.focus();
    if (prompt) {
      inp.value = prompt;
      runQuickCopilot(prompt);
    }
  }

  async function runQuickCopilot(q) {
    if (!q || !q.trim()) return;
    const results = $("#copilot-modal-results");
    results.innerHTML = `<div class="admin-empty-state">Analyzing cluster with AI Copilot…</div>`;
    try {
      const resp = await request("/api/v1/admin/ai/copilot", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ query: q }),
      });
      let matchesHtml = "";
      if (resp.matches && resp.matches.length > 0) {
        matchesHtml = `
          <div style="margin-top: 12px; max-height: 200px; overflow-y: auto;">
            <table class="admin-table">
              <thead><tr><th>Kind</th><th>Name</th><th>Status</th><th>Action</th></tr></thead>
              <tbody>
                ${resp.matches.slice(0, 8).map((m) => `
                  <tr>
                    <td><span class="admin-kind-badge">${escapeHTML(m.kind)}</span></td>
                    <td class="admin-table-name"><strong>${escapeHTML(m.name)}</strong></td>
                    <td><span class="admin-badge ${m.status === "CrashLoopBackOff" || m.status === "critical" ? "admin-badge-crashloop" : "admin-badge-warning"}">${escapeHTML(m.status)}</span></td>
                    <td>
                      ${m.link ? `<a class="admin-btn admin-btn-sm" href="${escapeHTML(m.link)}" onclick="$('#admin-copilot-modal').classList.remove('open')">View</a>` : ""}
                    </td>
                  </tr>
                `).join("")}
              </tbody>
            </table>
          </div>
        `;
      }
      results.innerHTML = `
        <div class="admin-ai-banner" style="margin-top: 8px;">
          <div style="font-size: 11px; font-weight: 700; color: var(--admin-accent); margin-bottom: 4px;">Intent: ${escapeHTML(resp.intent)}</div>
          <p style="margin: 0; font-size: 12px; line-height: 1.6; color: var(--admin-text);">${escapeHTML(resp.summary)}</p>
        </div>
        ${matchesHtml}
      `;
    } catch (err) {
      results.innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Copilot error: ${escapeHTML(err.message)}</div>`;
    }
  }

  $("#topbar-copilot-btn").addEventListener("click", () => openQuickCopilot());
  $("#copilot-modal-close").addEventListener("click", () => $("#admin-copilot-modal").classList.remove("open"));
  $("#copilot-modal-dismiss").addEventListener("click", () => $("#admin-copilot-modal").classList.remove("open"));
  $("#copilot-modal-form").addEventListener("submit", (e) => {
    e.preventDefault();
    runQuickCopilot($("#copilot-modal-input").value);
  });
  $$("#copilot-quick-prompts button").forEach((btn) => {
    btn.addEventListener("click", () => {
      $("#copilot-modal-input").value = btn.dataset.prompt;
      runQuickCopilot(btn.dataset.prompt);
    });
  });

  window.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      e.preventDefault();
      openCommandPalette();
    }
  });

  // Post-Mortem Modal
  $("#postmortem-modal-close")?.addEventListener("click", () => $("#admin-postmortem-modal").classList.remove("open"));
  $("#postmortem-modal-dismiss")?.addEventListener("click", () => $("#admin-postmortem-modal").classList.remove("open"));
  $("#postmortem-modal-copy-btn")?.addEventListener("click", copyPostMortemMarkdown);
  $("#btn-copy-postmortem-markdown")?.addEventListener("click", copyPostMortemMarkdown);

  $("#tab-btn-postmortem-preview")?.addEventListener("click", () => switchPostMortemTab("preview"));
  $("#tab-btn-postmortem-markdown")?.addEventListener("click", () => switchPostMortemTab("markdown"));
  $("#tab-btn-postmortem-runbook")?.addEventListener("click", () => switchPostMortemTab("runbook"));

  // Rollback Modal
  $("#rollback-modal-close")?.addEventListener("click", () => $("#admin-rollback-modal").classList.remove("open"));
  $("#rollback-modal-dismiss")?.addEventListener("click", () => $("#admin-rollback-modal").classList.remove("open"));

  // Drain Simulator Modal
  $("#drain-modal-close")?.addEventListener("click", () => $("#admin-drain-modal").classList.remove("open"));
  $("#drain-modal-dismiss")?.addEventListener("click", () => $("#admin-drain-modal").classList.remove("open"));

  // Webhook Modal
  $("#webhook-modal-close")?.addEventListener("click", () => $("#admin-webhook-modal").classList.remove("open"));
  $("#webhook-modal-dismiss")?.addEventListener("click", () => $("#admin-webhook-modal").classList.remove("open"));
  $("#webhook-form")?.addEventListener("submit", handleCreateWebhook);

  // Ephemeral Debug Modal
  $("#debug-modal-close")?.addEventListener("click", closeEphemeralDebugModal);
  $("#debug-modal-dismiss")?.addEventListener("click", closeEphemeralDebugModal);
  $("#debug-image-select")?.addEventListener("change", (e) => {
    const customWrap = $("#debug-custom-image-wrap");
    if (e.target.value === "custom") {
      customWrap.hidden = false;
      $("#debug-custom-image-input").focus();
    } else {
      customWrap.hidden = true;
    }
  });
  $("#debug-form")?.addEventListener("submit", handleLaunchDebugContainer);

  // Enterprise Suite Event Listeners
  $("#open-cmd-k-btn")?.addEventListener("click", () => openCommandPalette());
  $("#cmd-k-close-btn")?.addEventListener("click", () => closeCommandPalette());
  $("#admin-cmd-k-modal")?.addEventListener("click", (e) => {
    if (e.target.id === "admin-cmd-k-modal") closeCommandPalette();
  });
  $("#cmd-k-input")?.addEventListener("input", (e) => filterCommandPalette(e.target.value));
  $("#cmd-k-input")?.addEventListener("keydown", handleCommandPaletteKeydown);

  $("#helm-modal-close")?.addEventListener("click", () => $("#admin-helm-modal").classList.remove("open"));
  $("#tab-btn-helm-user")?.addEventListener("click", () => switchHelmValuesTab("user"));
  $("#tab-btn-helm-computed")?.addEventListener("click", () => switchHelmValuesTab("computed"));
  $("#helm-modal-copy-btn")?.addEventListener("click", copyHelmValuesToClipboard);

  $("#cve-modal-close")?.addEventListener("click", () => $("#admin-cve-modal").classList.remove("open"));

  // Sign out
  $("#admin-signout").addEventListener("click", async () => {
    await fetch("/auth/logout", { method: "POST", credentials: "same-origin" });
    window.location.assign("/login");
  });
}

// -------------------------------------------------------------
// Operational Intelligence Feature 1: Incident War Room
// -------------------------------------------------------------

async function renderWarRoom(container) {
  const hash = window.location.hash.split("?")[1] || "";
  const params = new URLSearchParams(hash);
  const ns = params.get("ns") || "";
  const pod = params.get("pod") || "";

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Incident War Room</h1>
        <p>Live incident triage, root-cause investigation, and shared war room console</p>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" id="btn-share-warroom" type="button">Copy War Room Link</button>
        <button class="admin-btn admin-btn-primary admin-btn-sm" id="btn-warroom-postmortem" type="button" disabled>Generate AI Post-Mortem</button>
      </div>
    </div>
    <div class="admin-empty-state">Connecting to active incident telemetry…</div>
  `;

  try {
    const url = `/api/v1/admin/incidents/snapshot?namespace=${encodeURIComponent(ns)}&pod=${encodeURIComponent(pod)}`;
    const snapshot = await request(url);

    const diag = snapshot.diagnosis || {};
    let exitBadge = "";
    if (snapshot.exit_code !== undefined && snapshot.exit_code !== 0) {
      exitBadge = `<span class="admin-badge admin-badge-crashloop">Exit ${escapeHTML(snapshot.exit_code)} (${escapeHTML(snapshot.reason || "Terminated")})</span>`;
    }

    let eventsHtml = `<div style="color: var(--admin-text-muted); font-size: 12px; padding: 10px 0;">No warning events recorded for this workload.</div>`;
    if (snapshot.warning_events && snapshot.warning_events.length > 0) {
      eventsHtml = `
        <table class="admin-table admin-timeline-table" style="font-size: 11px;">
          <thead>
            <tr>
              <th style="width: 140px;">Timestamp</th>
              <th style="width: 110px;">Reason</th>
              <th>Message</th>
            </tr>
          </thead>
          <tbody>
            ${snapshot.warning_events.map(ev => `
              <tr>
                <td style="color: var(--admin-text-muted);">${escapeHTML(ev.last_timestamp || ev.first_timestamp || "recently")}</td>
                <td><span class="admin-badge admin-badge-warning">${escapeHTML(ev.reason)}</span></td>
                <td>${escapeHTML(ev.message)}</td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      `;
    }

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <div style="display: flex; align-items: center; gap: 8px;">
            <span class="admin-badge admin-badge-danger" style="font-size: 11px;">INCIDENT WAR ROOM</span>
            <span style="font-family: var(--admin-mono); font-size: 12px; color: var(--admin-text-muted);">${escapeHTML(snapshot.incident_id)}</span>
          </div>
          <h1>${escapeHTML(snapshot.pod_name)}</h1>
          <p>Target Workload in namespace <strong>${escapeHTML(snapshot.namespace)}</strong> · Captured: ${escapeHTML(snapshot.captured_at)}</p>
        </div>
        <div class="admin-view-actions">
          <button class="admin-btn admin-btn-sm" id="btn-share-warroom" type="button">Copy War Room Link</button>
          <button class="admin-btn admin-btn-primary admin-btn-sm" id="btn-warroom-postmortem" type="button">Generate AI Post-Mortem</button>
        </div>
      </div>

      <!-- Incident KPI Banner -->
      <div class="admin-overview-grid" style="grid-template-columns: repeat(4, 1fr); margin-bottom: 16px;">
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Phase & State</span></div>
          <div class="admin-stat-value" style="font-size: 18px; color: var(--admin-red);">${escapeHTML(snapshot.phase)}</div>
          <div class="admin-stat-subtext">${exitBadge || "Non-zero exit"}</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Severity</span></div>
          <div class="admin-stat-value" style="font-size: 18px; text-transform: uppercase;">${escapeHTML(diag.severity || "CRITICAL")}</div>
          <div class="admin-stat-subtext">Confidence: ${escapeHTML(diag.confidence || "High")}</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Warning Events</span></div>
          <div class="admin-stat-value" style="font-size: 18px;">${snapshot.warning_events ? snapshot.warning_events.length : 0}</div>
          <div class="admin-stat-subtext">Cluster controller events</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Quick Remediations</span></div>
          <div style="margin-top: 10px; display: flex; gap: 6px;">
            <button class="admin-btn admin-btn-danger admin-btn-sm" type="button" onclick="confirmDeletePod('${escapeHTML(snapshot.namespace)}', '${escapeHTML(snapshot.pod_name)}')">Evict Pod</button>
            <button class="admin-btn admin-btn-sm" type="button" onclick="openInspector('pod', '${escapeHTML(snapshot.namespace)}', '${escapeHTML(snapshot.pod_name)}')">Inspect Pod</button>
          </div>
        </div>
      </div>

      <!-- Split Layout: RCA & Timeline vs Live Logs -->
      <div class="admin-warroom-split">
        <!-- Left: Diagnosis & Events -->
        <div>
          <div class="admin-ai-banner" style="margin-bottom: 14px;">
            <div class="admin-ai-banner-title">${escapeHTML(diag.summary || "Workload failure detected")}</div>
            <p style="margin: 6px 0 0; font-size: 12px; color: var(--admin-text); line-height: 1.5;">${escapeHTML(diag.probable_cause || "")}</p>
          </div>

          ${diag.log_anomaly ? `
            <div style="background: rgba(220, 38, 38, 0.08); border-left: 3px solid var(--admin-red); padding: 10px 14px; margin-bottom: 14px; border-radius: 4px;">
              <div style="font-size: 10px; font-weight: 700; text-transform: uppercase; color: var(--admin-red); margin-bottom: 4px;">Extracted Anomaly / Exception</div>
              <div style="font-family: var(--admin-mono); font-size: 11px; color: var(--admin-red); word-break: break-all;">${escapeHTML(diag.log_anomaly)}</div>
            </div>
          ` : ""}

          ${diag.stack_trace ? `
            <div class="admin-spec-group" style="margin-bottom: 14px;">
              <div class="admin-spec-group-title">Application Stack Trace</div>
              <pre class="admin-stacktrace-block">${escapeHTML(diag.stack_trace)}</pre>
            </div>
          ` : ""}

          <div class="admin-spec-group">
            <div class="admin-spec-group-title">Cluster Controller Events Timeline</div>
            <div class="admin-table-container" style="max-height: 260px; overflow-y: auto;">
              ${eventsHtml}
            </div>
          </div>
        </div>

        <!-- Right: Recent Container Logs Console -->
        <div>
          <div class="admin-warroom-console">
            <div class="admin-warroom-console-header">
              <span>Container Logs Tail (Incident Telemetry)</span>
              <button class="admin-btn admin-btn-sm" style="padding: 2px 8px; font-size: 10px;" type="button" onclick="navigator.clipboard.writeText($('#warroom-logs-code').textContent); this.textContent='✓ Copied'; setTimeout(() => this.textContent='Copy Logs', 1800);">Copy Logs</button>
            </div>
            <div class="admin-warroom-console-body" id="warroom-logs-code">${escapeHTML(snapshot.recent_logs || "No logs available for container.")}</div>
          </div>
        </div>
      </div>
    `;

    $("#btn-warroom-postmortem").onclick = () => openPostMortem(snapshot.namespace, snapshot.pod_name);
    $("#btn-share-warroom").onclick = () => {
      const fullUrl = `${window.location.origin}/admin#war-room?ns=${encodeURIComponent(snapshot.namespace)}&pod=${encodeURIComponent(snapshot.pod_name)}`;
      navigator.clipboard.writeText(fullUrl).then(() => {
        $("#btn-share-warroom").textContent = "✓ Link Copied";
        setTimeout(() => { $("#btn-share-warroom").textContent = "Copy War Room Link"; }, 2000);
      });
    };
  } catch (err) {
    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title"><h1>Incident War Room</h1></div>
      </div>
      <div class="admin-empty-state">
        <h2 style="font-size: 16px; margin-bottom: 8px;">No Active Workload Incidents Found</h2>
        <p style="color: var(--admin-text-muted); max-width: 460px; margin: 0 auto 16px;">
          All workloads are running smoothly. If you suspect a failure, navigate to <a href="#pods">Pods</a> and click "AI RCA" or "War Room" on any specific workload.
        </p>
        <a class="admin-btn admin-btn-primary" href="#pods">View Cluster Pods</a>
      </div>
    `;
  }
}

// -------------------------------------------------------------
// Operational Intelligence Feature 2: Platform Audit Trail
// -------------------------------------------------------------

async function renderAuditTrail(container) {
  state.auditNs = state.auditNs || "";
  state.auditAction = state.auditAction || "";

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Platform Action Audit Trail</h1>
        <p>Immutable forensic record of all administrative operations, rollouts, scaling, and security hardening executed on the platform</p>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" type="button" onclick="renderAuditTrail(document.getElementById('admin-content-area'))">Refresh Trail</button>
      </div>
    </div>
    <div class="admin-filter-bar" style="display: flex; gap: 10px; margin-bottom: 16px; align-items: center;">
      <select class="admin-select" id="audit-ns-select" style="min-width: 180px;">
        <option value="">All Namespaces</option>
        ${state.namespaces.map(ns => `<option value="${escapeHTML(ns)}" ${ns === state.auditNs ? "selected" : ""}>${escapeHTML(ns)}</option>`).join("")}
      </select>
      <select class="admin-select" id="audit-action-select" style="min-width: 160px;">
        <option value="">All Action Types</option>
        <option value="restart" ${state.auditAction === "restart" ? "selected" : ""}>restart</option>
        <option value="scale" ${state.auditAction === "scale" ? "selected" : ""}>scale</option>
        <option value="patch-env" ${state.auditAction === "patch-env" ? "selected" : ""}>patch-env</option>
        <option value="harden" ${state.auditAction === "harden" ? "selected" : ""}>harden</option>
        <option value="rightsize" ${state.auditAction === "rightsize" ? "selected" : ""}>rightsize</option>
        <option value="delete" ${state.auditAction === "delete" ? "selected" : ""}>delete</option>
        <option value="create" ${state.auditAction === "create" ? "selected" : ""}>create</option>
      </select>
      <input class="text-input" id="audit-search-filter" placeholder="Filter audit trail by operator or resource..." style="flex: 1; max-width: 320px;">
    </div>
    <div class="admin-table-container">
      <div class="admin-empty-state">Loading platform audit logs…</div>
    </div>
  `;

  $("#audit-ns-select").onchange = (e) => {
    state.auditNs = e.target.value;
    renderAuditTrail(container);
  };
  $("#audit-action-select").onchange = (e) => {
    state.auditAction = e.target.value;
    renderAuditTrail(container);
  };

  try {
    const logs = await request(`/api/v1/admin/audit-trail?namespace=${encodeURIComponent(state.auditNs)}&action_type=${encodeURIComponent(state.auditAction)}`);

    const tableWrap = container.querySelector(".admin-table-container");
    if (!logs || logs.length === 0) {
      tableWrap.innerHTML = `
        <div class="admin-empty-state">
          <h3>No Audit Records Found</h3>
          <p>No operator actions matching the selected filter criteria have been recorded yet.</p>
        </div>
      `;
      return;
    }

    function renderAuditTable(filteredLogs) {
      tableWrap.innerHTML = `
        <table class="admin-table">
          <thead>
            <tr>
              <th style="width: 160px;">Timestamp (UTC)</th>
              <th style="width: 180px;">Operator</th>
              <th style="width: 100px;">Action</th>
              <th style="width: 100px;">Resource Kind</th>
              <th style="width: 220px;">Namespace / Name</th>
              <th>Execution Details</th>
            </tr>
          </thead>
          <tbody>
            ${filteredLogs.map(l => `
              <tr>
                <td style="color: var(--admin-text-muted); font-size: 11px;">${escapeHTML(l.created_at)}</td>
                <td style="font-weight: 600;">${escapeHTML(l.operator_email)}</td>
                <td><span class="admin-audit-badge ${escapeHTML(l.action_type)}">${escapeHTML(l.action_type)}</span></td>
                <td><strong>${escapeHTML(l.resource_kind)}</strong></td>
                <td>
                  <span style="font-family: var(--admin-mono); font-size: 12px;">${l.namespace ? `${escapeHTML(l.namespace)}/` : ""}${escapeHTML(l.name)}</span>
                </td>
                <td style="color: var(--admin-text); font-size: 12px;">${escapeHTML(l.details || "—")}</td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      `;
    }

    renderAuditTable(logs);

    $("#audit-search-filter").oninput = (e) => {
      const q = e.target.value.toLowerCase().trim();
      if (!q) {
        renderAuditTable(logs);
      } else {
        const filtered = logs.filter(l => 
          (l.operator_email && l.operator_email.toLowerCase().includes(q)) ||
          (l.name && l.name.toLowerCase().includes(q)) ||
          (l.namespace && l.namespace.toLowerCase().includes(q)) ||
          (l.details && l.details.toLowerCase().includes(q)) ||
          (l.action_type && l.action_type.toLowerCase().includes(q))
        );
        renderAuditTable(filtered);
      }
    };
  } catch (err) {
    container.querySelector(".admin-table-container").innerHTML = `
      <div class="admin-empty-state" style="color: var(--admin-red);">Failed to load audit trail: ${escapeHTML(err.message)}</div>
    `;
  }
}

// -------------------------------------------------------------
// Operational Intelligence Feature 3: Cluster Upgrade Radar
// -------------------------------------------------------------

async function renderUpgradeRadar(container) {
  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Cluster Upgrade Radar & API Deprecation Scanner</h1>
        <p>Continuous compatibility assessment for upcoming Kubernetes releases and breaking API removals</p>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" type="button" onclick="renderUpgradeRadar(document.getElementById('admin-content-area'))">Rescan Cluster</button>
      </div>
    </div>
    <div class="admin-empty-state">Evaluating cluster APIs against enterprise Kubernetes target version…</div>
  `;

  try {
    const radar = await request("/api/v1/admin/cluster/upgrade-radar");

    let dialClass = "admin-radar-score-dial";
    if (radar.upgrade_readiness_score < 70) {
      dialClass += " danger";
    } else if (radar.upgrade_readiness_score < 90) {
      dialClass += " warning";
    }

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Cluster Upgrade Radar & API Deprecation Scanner</h1>
          <p>Continuous compatibility assessment for upcoming Kubernetes releases and breaking API removals</p>
        </div>
        <div class="admin-view-actions">
          <button class="admin-btn admin-btn-sm" type="button" onclick="renderUpgradeRadar(document.getElementById('admin-content-area'))">Rescan Cluster</button>
        </div>
      </div>

      <!-- Hero Dial -->
      <div class="admin-radar-hero">
        <div class="${dialClass}">
          ${radar.upgrade_readiness_score}%
        </div>
        <div style="flex: 1;">
          <div style="font-size: 11px; text-transform: uppercase; font-weight: 700; color: var(--admin-text-muted); margin-bottom: 4px;">Upgrade Readiness Score</div>
          <h2 style="margin: 0 0 6px; font-family: var(--admin-font-heading); font-size: 20px;">
            Current: <span style="font-family: var(--admin-mono);">${escapeHTML(radar.current_server_version)}</span> → Target: <span style="font-family: var(--admin-mono); color: var(--admin-accent);">${escapeHTML(radar.target_version)}</span>
          </h2>
          <p style="margin: 0; color: var(--admin-text-muted); font-size: 13px;">
            ${radar.critical_blockers_count > 0 
              ? `<strong style="color: var(--admin-red);">${radar.critical_blockers_count} critical upgrade blocker(s)</strong> must be remediated before initiating control-plane upgrade.` 
              : (radar.deprecated_count > 0 ? `Cluster has ${radar.deprecated_count} deprecated API usage(s) flagged for migration.` : "All workloads comply with target Kubernetes APIs. Zero upgrade blockers detected.")}
          </p>
        </div>
        <div style="display: flex; gap: 14px; text-align: center;">
          <div style="padding: 10px 16px; background: #ffffff; border: 1px solid var(--admin-border); border-radius: 8px;">
            <div style="font-size: 22px; font-weight: 700; color: var(--admin-text);">${radar.deprecated_count}</div>
            <div style="font-size: 10px; text-transform: uppercase; color: var(--admin-text-muted);">Deprecated APIs</div>
          </div>
          <div style="padding: 10px 16px; background: #ffffff; border: 1px solid var(--admin-border); border-radius: 8px;">
            <div style="font-size: 22px; font-weight: 700; color: ${radar.critical_blockers_count > 0 ? "var(--admin-red)" : "var(--admin-green);"}">${radar.critical_blockers_count}</div>
            <div style="font-size: 10px; text-transform: uppercase; color: var(--admin-text-muted);">Critical Blockers</div>
          </div>
        </div>
      </div>

      <!-- Deprecations Table -->
      <div class="admin-table-container">
        <div style="padding: 14px 18px; border-bottom: 1px solid var(--admin-border); background: #ffffff; display: flex; justify-content: space-between; align-items: center;">
          <h3 style="margin: 0; font-size: 14px;">Detected Deprecated APIs & Migration Action Plan</h3>
        </div>
        ${(!radar.findings || radar.findings.length === 0) ? `
          <div class="admin-empty-state">
            <h3 style="color: var(--admin-green);">No Deprecated APIs Detected</h3>
            <p>Your cluster workloads are fully aligned with modern Kubernetes API standards.</p>
          </div>
        ` : `
          <table class="admin-table">
            <thead>
              <tr>
                <th style="width: 100px;">Kind</th>
                <th style="width: 180px;">Resource</th>
                <th style="width: 140px;">Current Version / Field</th>
                <th style="width: 100px;">Removed In</th>
                <th style="width: 140px;">Replacement</th>
                <th style="width: 90px;">Severity</th>
                <th>Remediation Guide</th>
              </tr>
            </thead>
            <tbody>
              ${radar.findings.map(f => `
                <tr>
                  <td><strong>${escapeHTML(f.kind)}</strong></td>
                  <td>
                    <span style="font-family: var(--admin-mono); font-size: 12px;">${f.namespace ? `${escapeHTML(f.namespace)}/` : ""}${escapeHTML(f.name)}</span>
                  </td>
                  <td><span class="admin-badge admin-badge-warning">${escapeHTML(f.current_api_version)}</span></td>
                  <td><span class="admin-badge admin-badge-danger">${escapeHTML(f.removed_in)}</span></td>
                  <td><span style="font-family: var(--admin-mono); font-size: 11px;">${escapeHTML(f.replacement_gvk)}</span></td>
                  <td><span class="admin-badge ${f.severity === "critical" ? "admin-badge-danger" : "admin-badge-warning"}">${escapeHTML(f.severity.toUpperCase())}</span></td>
                  <td style="font-size: 12px; color: var(--admin-text);">${escapeHTML(f.migration_steps)}</td>
                </tr>
              `).join("")}
            </tbody>
          </table>
        `}
      </div>
    `;
  } catch (err) {
    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title"><h1>Cluster Upgrade Radar</h1></div>
      </div>
      <div class="admin-empty-state" style="color: var(--admin-red);">Failed to run upgrade radar: ${escapeHTML(err.message)}</div>
    `;
  }
}

// -------------------------------------------------------------
// Operational Intelligence Feature 4: Visual Traffic Topology
// -------------------------------------------------------------

async function renderNetworkTopology(container) {
  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Visual Traffic & Ingress Routing Topology</h1>
        <p>End-to-end traffic routing mapping from public Ingress domains through Services to healthy Pod endpoints</p>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" type="button" onclick="renderNetworkTopology(document.getElementById('admin-content-area'))">Refresh Topology</button>
      </div>
    </div>
    <div class="admin-empty-state">Mapping network traffic routes…</div>
  `;

  try {
    const topo = await request("/api/v1/admin/network/topology");

    let orphanAlertsHtml = "";
    if (topo.orphan_alerts && topo.orphan_alerts.length > 0) {
      orphanAlertsHtml = `
        <div style="background: rgba(220, 38, 38, 0.08); border-left: 4px solid var(--admin-red); padding: 14px 18px; margin-bottom: 20px; border-radius: 6px;">
          <div style="font-weight: 700; color: var(--admin-red); margin-bottom: 6px; font-size: 13px;">Orphan Service Warning: Traffic Black Hole Detected</div>
          <ul style="margin: 0; padding-left: 20px; font-size: 12px; color: var(--admin-text);">
            ${topo.orphan_alerts.map(a => `
              <li style="margin-bottom: 4px;">
                <strong>${escapeHTML(a.namespace)}/${escapeHTML(a.service_name)}</strong>: ${escapeHTML(a.reason)} (${escapeHTML(a.remediation)})
              </li>
            `).join("")}
          </ul>
        </div>
      `;
    }

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Visual Traffic & Ingress Routing Topology</h1>
          <p>End-to-end traffic routing mapping from public Ingress domains through Services to healthy Pod endpoints</p>
        </div>
        <div class="admin-view-actions">
          <button class="admin-btn admin-btn-sm" type="button" onclick="renderNetworkTopology(document.getElementById('admin-content-area'))">Refresh Topology</button>
        </div>
      </div>

      <!-- KPI Summary -->
      <div class="admin-overview-grid" style="grid-template-columns: repeat(4, 1fr); margin-bottom: 20px;">
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Ingress Gateways</span></div>
          <div class="admin-stat-value">${topo.total_ingresses || 0}</div>
          <div class="admin-stat-subtext">Active ingress rules</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Cluster Services</span></div>
          <div class="admin-stat-value">${topo.total_services || 0}</div>
          <div class="admin-stat-subtext">ClusterIP & LoadBalancer</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Live Pod Endpoints</span></div>
          <div class="admin-stat-value">${topo.total_endpoints || 0}</div>
          <div class="admin-stat-subtext">Target backend replicas</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Orphan Services</span></div>
          <div class="admin-stat-value" style="color: ${topo.orphan_services_count > 0 ? "var(--admin-red)" : "var(--admin-green);"}">${topo.orphan_services_count || 0}</div>
          <div class="admin-stat-subtext">${topo.orphan_services_count > 0 ? "0 matching backend pods" : "All services routed"}</div>
        </div>
      </div>

      ${orphanAlertsHtml}

      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 14px;">
        <h3 style="margin: 0; font-size: 15px;">Live Traffic Pipelines</h3>
        <input class="text-input" id="topology-search" placeholder="Filter routes by domain, path, or service..." style="width: 280px; font-size: 12px;">
      </div>

      <div id="topology-cards-container">
        ${(!topo.routes || topo.routes.length === 0) ? `
          <div class="admin-empty-state">No ingress traffic routes detected in cluster.</div>
        ` : topo.routes.map(r => `
          <div class="admin-topology-card" data-search="${escapeHTML(r.host + ' ' + r.path + ' ' + r.service_name).toLowerCase()}">
            <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 6px;">
              <span style="font-size: 11px; font-weight: 700; color: var(--admin-text-muted); text-transform: uppercase;">
                Namespace: <strong style="color: var(--admin-text);">${escapeHTML(r.namespace)}</strong>
              </span>
              <span class="admin-badge ${r.tls ? "admin-badge-success" : "admin-badge-warning"}">
                ${r.tls ? "TLS / HTTPS" : "HTTP (Insecure)"}
              </span>
            </div>
            <div class="admin-topology-flow">
              <!-- Ingress Node -->
              <div class="admin-topology-node ingress">
                <div style="font-size: 10px; font-weight: 700; text-transform: uppercase; color: #2563eb;">Ingress Gateway</div>
                <div style="font-family: var(--admin-mono); font-weight: 700; font-size: 13px; color: var(--admin-text); margin: 2px 0;">${escapeHTML(r.host)}</div>
                <div style="font-family: var(--admin-mono); font-size: 11px; color: var(--admin-text-muted);">Path: ${escapeHTML(r.path)} (${escapeHTML(r.path_type)})</div>
              </div>

              <div class="admin-topology-arrow">→</div>

              <!-- Service Node -->
              <div class="admin-topology-node service">
                <div style="font-size: 10px; font-weight: 700; text-transform: uppercase; color: #0d9488;">Kubernetes Service</div>
                <div style="font-family: var(--admin-mono); font-weight: 700; font-size: 13px; color: var(--admin-text); margin: 2px 0;">${escapeHTML(r.service_name || "—")}</div>
                <div style="font-family: var(--admin-mono); font-size: 11px; color: var(--admin-text-muted);">Port: ${escapeHTML(r.service_port)} · ${escapeHTML(r.service_type)}</div>
              </div>

              <div class="admin-topology-arrow">→</div>

              <!-- Target Pod Endpoints -->
              <div class="admin-topology-node pod" style="flex: 1; min-width: 240px;">
                <div style="font-size: 10px; font-weight: 700; text-transform: uppercase; color: #16a34a; margin-bottom: 4px;">Target Pod Endpoints (${r.target_pods ? r.target_pods.length : 0})</div>
                ${(!r.target_pods || r.target_pods.length === 0) ? `
                  <div style="color: var(--admin-red); font-size: 11px; font-weight: 600;">No running pods match this service selector</div>
                ` : `
                  <div style="display: flex; flex-wrap: wrap; gap: 6px;">
                    ${r.target_pods.map(p => `
                      <span class="admin-tag-chip" style="cursor: pointer; display: inline-flex; align-items: center; gap: 4px;" onclick="openInspector('pod', '${escapeHTML(r.namespace)}', '${escapeHTML(p.pod_name)}')">
                        <span style="display: inline-block; width: 6px; height: 6px; border-radius: 50%; background: ${p.ready ? "var(--admin-green)" : "var(--admin-red);"}"></span>
                        ${escapeHTML(p.pod_name)} (${escapeHTML(p.pod_ip || "pending")})
                      </span>
                    `).join("")}
                  </div>
                `}
              </div>
            </div>
          </div>
        `).join("")}
      </div>
    `;

    $("#topology-search").oninput = (e) => {
      const q = e.target.value.toLowerCase().trim();
      $$("#topology-cards-container .admin-topology-card").forEach(card => {
        if (!q || card.dataset.search.includes(q)) {
          card.style.display = "";
        } else {
          card.style.display = "none";
        }
      });
    };
  } catch (err) {
    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title"><h1>Visual Traffic Topology</h1></div>
      </div>
      <div class="admin-empty-state" style="color: var(--admin-red);">Failed to map network topology: ${escapeHTML(err.message)}</div>
    `;
  }
}

// -------------------------------------------------------------
// Operational Intelligence Feature 5: Tenant Isolation Matrix
// -------------------------------------------------------------

async function renderTenantIsolationMatrix(container) {
  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Multi-Tenant Network Firewall & Isolation Matrix</h1>
        <p>Capsule tenant boundary checks and NetworkPolicy zero-trust enforcement across namespaces</p>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" type="button" onclick="renderTenantIsolationMatrix(document.getElementById('admin-content-area'))">Rescan Matrix</button>
      </div>
    </div>
    <div class="admin-empty-state">Analyzing cross-namespace network boundary enforcement…</div>
  `;

  try {
    const matrix = await request("/api/v1/admin/network/matrix");

    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title">
          <h1>Multi-Tenant Network Firewall & Isolation Matrix</h1>
          <p>Capsule tenant boundary checks and NetworkPolicy zero-trust enforcement across namespaces</p>
        </div>
        <div class="admin-view-actions">
          <button class="admin-btn admin-btn-sm" type="button" onclick="renderTenantIsolationMatrix(document.getElementById('admin-content-area'))">Rescan Matrix</button>
        </div>
      </div>

      <!-- KPI Summary -->
      <div class="admin-overview-grid" style="grid-template-columns: repeat(4, 1fr); margin-bottom: 20px;">
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Cluster Isolation Score</span></div>
          <div class="admin-stat-value" style="color: ${matrix.isolation_score >= 80 ? "var(--admin-green)" : (matrix.isolation_score >= 50 ? "var(--admin-amber)" : "var(--admin-red);")}">${matrix.isolation_score || 0}%</div>
          <div class="admin-stat-subtext">Zero-trust network posture</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Fully Isolated Tenants</span></div>
          <div class="admin-stat-value" style="color: var(--admin-green);">${matrix.fully_isolated_tenants || 0}</div>
          <div class="admin-stat-subtext">Default-deny ingress & egress</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Partially Isolated</span></div>
          <div class="admin-stat-value" style="color: var(--admin-amber);">${matrix.partially_isolated || 0}</div>
          <div class="admin-stat-subtext">Partial policy coverage</div>
        </div>
        <div class="admin-stat-card">
          <div class="admin-stat-header"><span>Unisolated Tenants</span></div>
          <div class="admin-stat-value" style="color: ${matrix.unisolated_tenants > 0 ? "var(--admin-red)" : "var(--admin-green);"}">${matrix.unisolated_tenants || 0}</div>
          <div class="admin-stat-subtext">Open East-West traffic</div>
        </div>
      </div>

      <!-- Matrix Table -->
      <div class="admin-table-container">
        <table class="admin-table">
          <thead>
            <tr>
              <th style="width: 160px;">Namespace</th>
              <th style="width: 150px;">Capsule Tenant</th>
              <th style="width: 130px;">Default Deny Ingress</th>
              <th style="width: 130px;">Default Deny Egress</th>
              <th>Cross-Namespace Peer Allowlist</th>
              <th style="width: 90px;">Policies</th>
              <th style="width: 150px;">Isolation Status</th>
            </tr>
          </thead>
          <tbody>
            ${(!matrix.tenants || matrix.tenants.length === 0) ? `
              <tr><td colspan="7" class="admin-empty-state">No tenant namespaces found.</td></tr>
            ` : matrix.tenants.map(t => `
              <tr>
                <td><strong>${escapeHTML(t.namespace)}</strong></td>
                <td><span class="admin-tag-chip">${escapeHTML(t.tenant_name)}</span></td>
                <td>
                  <span class="admin-badge ${t.has_default_deny_ingress ? "admin-badge-success" : "admin-badge-danger"}">
                    ${t.has_default_deny_ingress ? "Enforced" : "Open"}
                  </span>
                </td>
                <td>
                  <span class="admin-badge ${t.has_default_deny_egress ? "admin-badge-success" : "admin-badge-warning"}">
                    ${t.has_default_deny_egress ? "Enforced" : "Open"}
                  </span>
                </td>
                <td style="font-family: var(--admin-mono); font-size: 11px;">
                  ${t.allowed_cross_namespaces && t.allowed_cross_namespaces.length > 0 
                    ? t.allowed_cross_namespaces.map(peer => `<span class="admin-tag-chip" style="margin-right: 4px;">${escapeHTML(peer)}</span>`).join("") 
                    : `<span style="color: var(--admin-text-muted);">Strictly Blocked</span>`}
                </td>
                <td><strong>${t.policy_count || 0}</strong></td>
                <td>
                  <span class="admin-badge ${t.has_default_deny_ingress && t.has_default_deny_egress ? "admin-badge-success" : (t.has_default_deny_ingress ? "admin-badge-warning" : "admin-badge-danger")}">
                    ${escapeHTML(t.isolation_status)}
                  </span>
                </td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      </div>
    `;
  } catch (err) {
    container.innerHTML = `
      <div class="admin-view-header">
        <div class="admin-view-title"><h1>Multi-Tenant Network Firewall</h1></div>
      </div>
      <div class="admin-empty-state" style="color: var(--admin-red);">Failed to calculate isolation matrix: ${escapeHTML(err.message)}</div>
    `;
  }
}

// -------------------------------------------------------------
// Operational Intelligence Feature 6: AI Post-Mortem Modal Logic
// -------------------------------------------------------------

async function openPostMortem(namespace, podName) {
  const modal = $("#admin-postmortem-modal");
  modal.classList.add("open");
  switchPostMortemTab("preview");

  $("#postmortem-preview-body").innerHTML = `<div class="admin-empty-state">Synthesizing AI Post-Mortem & Preventative Runbook for ${escapeHTML(podName)}…</div>`;
  $("#postmortem-markdown-textarea").value = "";
  $("#postmortem-runbook-body").innerHTML = "";

  try {
    const report = await request(`/api/v1/admin/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}/post-mortem`);

    $("#postmortem-modal-title").textContent = report.title || "Incident Post-Mortem";
    $("#postmortem-modal-severity").textContent = report.severity || "CRITICAL";
    $("#postmortem-modal-id").textContent = report.incident_id || "INC-POSTMORTEM";
    $("#postmortem-markdown-textarea").value = report.markdown_content || "";

    // Tab 1: Preview
    $("#postmortem-preview-body").innerHTML = `
      <div class="admin-ai-banner">
        <div class="admin-ai-banner-title">Executive Summary</div>
        <p style="margin: 0; font-size: 13px; line-height: 1.6; color: var(--admin-text);">${escapeHTML(report.executive_summary)}</p>
      </div>

      <div class="admin-spec-group">
        <div class="admin-spec-group-title">Incident Chronological Timeline</div>
        <table class="admin-table admin-timeline-table" style="font-size: 11px;">
          <thead>
            <tr>
              <th style="width: 160px;">Timestamp (UTC)</th>
              <th style="width: 140px;">Source</th>
              <th>Event Description</th>
            </tr>
          </thead>
          <tbody>
            ${(report.timeline || []).map(t => `
              <tr>
                <td style="color: var(--admin-text-muted); font-family: var(--admin-mono);">${escapeHTML(t.timestamp)}</td>
                <td><strong>${escapeHTML(t.source)}</strong></td>
                <td>${escapeHTML(t.event)}</td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      </div>

      <div class="admin-ai-cause-box">
        <div class="admin-ai-cause-title">Technical Root Cause Analysis</div>
        <p style="margin: 0 0 8px; font-size: 12px; line-height: 1.6; color: var(--admin-text);">${escapeHTML(report.root_cause_technical)}</p>
        ${report.stack_trace ? `
          <div style="font-size: 10px; font-weight: 700; text-transform: uppercase; color: var(--admin-red); margin-bottom: 4px;">Captured Stack Trace</div>
          <pre class="admin-stacktrace-block">${escapeHTML(report.stack_trace)}</pre>
        ` : ""}
      </div>

      <div class="admin-spec-group">
        <div class="admin-spec-group-title">Multi-Tenant Impact & Blast Radius</div>
        <p style="font-size: 12px; color: var(--admin-text); margin: 0;">${escapeHTML(report.blast_radius)}</p>
      </div>
    `;

    // Tab 3: Preventative Runbook
    $("#postmortem-runbook-body").innerHTML = `
      <div style="margin-bottom: 14px; font-size: 13px; color: var(--admin-text-muted);">Action items synthesized to prevent workload recurrence:</div>
      <div style="display: flex; flex-direction: column; gap: 8px;">
        ${(report.preventative_runbook || []).map((step, idx) => `
          <div style="display: flex; align-items: flex-start; gap: 10px; padding: 10px 14px; background: #f8fafc; border: 1px solid var(--admin-border); border-radius: 6px;">
            <input type="checkbox" id="runbook-chk-${idx}" style="margin-top: 3px;">
            <label for="runbook-chk-${idx}" style="font-size: 12px; color: var(--admin-text); cursor: pointer; flex: 1;">${escapeHTML(step)}</label>
          </div>
        `).join("")}
      </div>
    `;
  } catch (err) {
    $("#postmortem-preview-body").innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Failed to generate post-mortem: ${escapeHTML(err.message)}</div>`;
  }
}

function switchPostMortemTab(tab) {
  $("#tab-btn-postmortem-preview")?.classList.toggle("active", tab === "preview");
  $("#tab-btn-postmortem-markdown")?.classList.toggle("active", tab === "markdown");
  $("#tab-btn-postmortem-runbook")?.classList.toggle("active", tab === "runbook");

  $("#postmortem-tab-preview").hidden = (tab !== "preview");
  $("#postmortem-tab-markdown").hidden = (tab !== "markdown");
  $("#postmortem-tab-runbook").hidden = (tab !== "runbook");
}

function copyPostMortemMarkdown() {
  const text = $("#postmortem-markdown-textarea").value;
  if (!text) return;
  navigator.clipboard.writeText(text).then(() => {
    const btn1 = $("#btn-copy-postmortem-markdown");
    const btn2 = $("#postmortem-modal-copy-btn");
    if (btn1) btn1.textContent = "✓ Copied to Clipboard!";
    if (btn2) btn2.textContent = "✓ Copied to Clipboard!";
    setTimeout(() => {
      if (btn1) btn1.textContent = "Copy Markdown to Clipboard";
      if (btn2) btn2.textContent = "1-Click Copy Markdown";
    }, 2000);
  });
}

// -------------------------------------------------------------
// Day-2 Superpower 1: Deployment Revision History & 1-Click Rollback
// -------------------------------------------------------------

let currentRollbackState = { namespace: "", deployment: "", selectedRevision: null };

async function openRollbackModal(namespace, deploymentName) {
  currentRollbackState = { namespace, deployment: deploymentName, selectedRevision: null };
  const modal = $("#admin-rollback-modal");
  modal.classList.add("open");
  $("#rollback-modal-title").textContent = `Rollback: ${deploymentName} (${namespace})`;
  $("#rollback-modal-status").textContent = "";
  $("#rollback-modal-confirm-btn").disabled = true;
  $("#rollback-revisions-list").innerHTML = `<div class="admin-empty-state">Fetching revisions…</div>`;
  $("#rollback-diff-pane").innerHTML = `<div class="admin-empty-state">Select a revision on the left to see differences against the active rollout.</div>`;

  try {
    const revs = await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(deploymentName)}/revisions`);
    if (!revs || revs.length === 0) {
      $("#rollback-revisions-list").innerHTML = `<div class="admin-empty-state">No ReplicaSet revisions found.</div>`;
      return;
    }

    $("#rollback-revisions-list").innerHTML = `
      <div class="admin-rev-list">
        ${revs.map((r) => `
          <div class="admin-rev-item ${r.is_current ? "active" : ""}" id="rev-item-${r.revision}" onclick="selectRollbackRevision('${escapeHTML(namespace)}', '${escapeHTML(deploymentName)}', ${r.revision})">
            <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 4px;">
              <strong style="font-size: 13px;">Revision ${r.revision}</strong>
              ${r.is_current ? `<span class="admin-badge admin-badge-ready" style="font-size: 10px;">ACTIVE</span>` : `<span style="font-size: 11px; color: var(--admin-text-muted);">${escapeHTML(r.age)} ago</span>`}
            </div>
            <div style="font-size: 11px; color: var(--admin-text-muted); word-break: break-all;">
              ${escapeHTML(r.images.join(", "))}
            </div>
          </div>
        `).join("")}
      </div>
    `;

    const previous = revs.find(r => !r.is_current);
    if (previous) {
      selectRollbackRevision(namespace, deploymentName, previous.revision);
    }
  } catch (err) {
    $("#rollback-revisions-list").innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Failed to fetch revisions: ${escapeHTML(err.message)}</div>`;
  }
}

async function selectRollbackRevision(namespace, deploymentName, revision) {
  currentRollbackState.selectedRevision = revision;
  $$(".admin-rev-item").forEach(el => el.classList.remove("active"));
  $(`#rev-item-${revision}`)?.classList.add("active");

  const diffPane = $("#rollback-diff-pane");
  diffPane.innerHTML = `<div class="admin-empty-state">Calculating visual diff…</div>`;
  $("#rollback-modal-confirm-btn").disabled = true;

  try {
    const diff = await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(deploymentName)}/revisions/${revision}/diff`);
    
    if (diff.identical) {
      diffPane.innerHTML = `
        <div class="admin-empty-state" style="color: var(--admin-green);">
          <h3>✓ Revision ${revision} is identical to active revision (${diff.current_revision})</h3>
          <p>No changes would be applied by rolling back to this revision.</p>
        </div>
      `;
      $("#rollback-modal-confirm-btn").disabled = true;
      return;
    }

    diffPane.innerHTML = `
      <div style="margin-bottom: 12px; display: flex; justify-content: space-between; align-items: center;">
        <span style="font-size: 12px; font-weight: 600;">Diff: Revision ${revision} (Target) vs Revision ${diff.current_revision} (Active)</span>
        <span class="admin-badge admin-badge-warning">${diff.changes.length} change(s)</span>
      </div>
      <div style="display: flex; flex-direction: column; gap: 8px;">
        ${diff.changes.map(c => `
          <div class="admin-diff-change-row">
            <div style="display: flex; justify-content: space-between; align-items: center;">
              <strong style="color: var(--admin-text);">${escapeHTML(c.field)}</strong>
              <span class="admin-diff-tag ${escapeHTML(c.action)}">${escapeHTML(c.action)}</span>
            </div>
            <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 8px; margin-top: 4px;">
              <div style="background: #fee2e2; color: #991b1b; padding: 4px 8px; border-radius: 4px; overflow-x: auto;">
                <span style="font-size: 10px; opacity: 0.7; display: block;">Active Spec (Revision ${diff.current_revision}):</span>
                ${escapeHTML(c.old_value)}
              </div>
              <div style="background: #dcfce7; color: #166534; padding: 4px 8px; border-radius: 4px; overflow-x: auto;">
                <span style="font-size: 10px; opacity: 0.7; display: block;">Target Spec (Revision ${revision}):</span>
                ${escapeHTML(c.new_value)}
              </div>
            </div>
          </div>
        `).join("")}
      </div>
    `;

    $("#rollback-modal-confirm-btn").disabled = false;
    $("#rollback-modal-confirm-btn").onclick = () => executeRollback(namespace, deploymentName, revision);
  } catch (err) {
    diffPane.innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Failed to compute diff: ${escapeHTML(err.message)}</div>`;
  }
}

async function executeRollback(namespace, deploymentName, targetRevision) {
  const btn = $("#rollback-modal-confirm-btn");
  btn.disabled = true;
  btn.textContent = "Rolling back…";
  $("#rollback-modal-status").textContent = "Triggering rolling deployment rollback…";

  try {
    await request(`/api/v1/admin/deployments/${encodeURIComponent(namespace)}/${encodeURIComponent(deploymentName)}/rollback`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ targetRevision }),
    });

    $("#rollback-modal-status").innerHTML = `<strong style="color: var(--admin-green);">✓ Successfully rolled back to revision ${targetRevision}!</strong>`;
    setTimeout(() => {
      $("#admin-rollback-modal").classList.remove("open");
      btn.textContent = "1-Click Rollback";
      if (state.activeView === "deployments") {
        renderDeployments($("#admin-content-area"));
      } else {
        renderCurrentView();
      }
    }, 1500);
  } catch (err) {
    btn.disabled = false;
    btn.textContent = "1-Click Rollback";
    $("#rollback-modal-status").innerHTML = `<span style="color: var(--admin-red);">Rollback failed: ${escapeHTML(err.message)}</span>`;
  }
}

// -------------------------------------------------------------
// Day-2 Superpower 2: TLS/SSL Certificate Expiration Radar
// -------------------------------------------------------------

async function renderCertificates(container) {
  const report = await request("/api/v1/admin/security/certificates");
  const filtered = filterRows(report.certificates || [], ["secret_name", "namespace", "common_name", "issuer", "status"]);

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>TLS/SSL Certificate Expiration Radar</h1>
        <span class="admin-view-count">${report.total_certificates}</span>
      </div>
    </div>

    <!-- Metrics Cards -->
    <div class="admin-kpi-grid" style="grid-template-columns: repeat(4, 1fr); margin-bottom: 20px;">
      <div class="admin-kpi-card">
        <div class="admin-kpi-title">Valid Certificates</div>
        <div class="admin-kpi-val" style="color: var(--admin-green);">${report.valid_count}</div>
        <div class="admin-kpi-sub">> 30 days remaining</div>
      </div>
      <div class="admin-kpi-card">
        <div class="admin-kpi-title">Expiring Soon</div>
        <div class="admin-kpi-val" style="color: var(--admin-amber);">${report.expiring_soon_count}</div>
        <div class="admin-kpi-sub">Within 30 days</div>
      </div>
      <div class="admin-kpi-card">
        <div class="admin-kpi-title">Critical Urgency</div>
        <div class="admin-kpi-val" style="color: var(--admin-red);">${report.critical_count}</div>
        <div class="admin-kpi-sub">Within 7 days</div>
      </div>
      <div class="admin-kpi-card">
        <div class="admin-kpi-title">Expired Certificates</div>
        <div class="admin-kpi-val" style="color: #64748b;">${report.expired_count}</div>
        <div class="admin-kpi-sub">Immediate outage risk</div>
      </div>
    </div>

    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Secret / Cert</th>
            <th>Namespace</th>
            <th>Common Name (CN)</th>
            <th>Subject Alternative Names (SANs)</th>
            <th>Issuer</th>
            <th>Expires On</th>
            <th>Days Remaining</th>
            <th>Status</th>
          </tr>
        </thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="8" class="admin-empty-state">No TLS secret certificates found in cluster.</td></tr>` : ""}
          ${filtered.map(c => {
            let statusBadge = `<span class="admin-badge admin-badge-ready">Valid</span>`;
            if (c.status === "expiring_soon") statusBadge = `<span class="admin-badge admin-badge-warning">Expiring Soon</span>`;
            if (c.status === "critical") statusBadge = `<span class="admin-badge admin-badge-danger">Critical (&lt; 7d)</span>`;
            if (c.status === "expired") statusBadge = `<span class="admin-badge" style="background:#fee2e2;color:#991b1b;border:1px solid #f87171;">Expired</span>`;

            return `
              <tr>
                <td class="admin-table-name">${escapeHTML(c.secret_name)}</td>
                <td class="admin-table-ns">${escapeHTML(c.namespace)}</td>
                <td><strong>${escapeHTML(c.common_name)}</strong></td>
                <td style="font-size: 11px; max-width: 250px; overflow: hidden; text-overflow: ellipsis;">
                  ${escapeHTML((c.dns_names || []).join(", ") || "-")}
                </td>
                <td style="font-size: 11px; color: var(--admin-text-muted);">${escapeHTML(c.issuer)} ${c.is_self_signed ? `<span class="admin-badge" style="font-size: 9px;">Self-Signed</span>` : ""}</td>
                <td style="font-size: 11px; font-family: var(--admin-mono);">${escapeHTML(c.not_after)}</td>
                <td>
                  <strong style="color: ${c.days_remaining <= 7 ? "var(--admin-red)" : c.days_remaining <= 30 ? "var(--admin-amber)" : "var(--admin-green)"}">
                    ${c.days_remaining <= 0 ? "EXPIRED" : `${c.days_remaining} days`}
                  </strong>
                </td>
                <td>${statusBadge}</td>
              </tr>
            `;
          }).join("")}
        </tbody>
      </table>
    </div>
  `;
}

// -------------------------------------------------------------
// Day-2 Superpower 3: Node Drain & Maintenance Safety Simulator
// -------------------------------------------------------------

async function openNodeDrainModal(nodeName) {
  const modal = $("#admin-drain-modal");
  modal.classList.add("open");
  $("#drain-modal-title").textContent = `Drain Simulation: ${nodeName}`;
  $("#drain-modal-status").textContent = "";
  $("#drain-modal-body").innerHTML = `<div class="admin-empty-state">Running live capacity simulation for ${escapeHTML(nodeName)}…</div>`;

  try {
    const sim = await request(`/api/v1/admin/nodes/${encodeURIComponent(nodeName)}/drain-simulation`);

    const isSafe = sim.status === "safe";
    const statusColor = isSafe ? "var(--admin-green)" : sim.status === "warning" ? "var(--admin-amber)" : "var(--admin-red)";
    $("#drain-modal-badge").textContent = sim.status.toUpperCase();
    $("#drain-modal-badge").style.borderColor = statusColor;
    $("#drain-modal-badge").style.color = statusColor;

    const cordonBtn = $("#drain-modal-cordon-btn");
    if (sim.unschedulable) {
      cordonBtn.textContent = "Uncordon Node";
      cordonBtn.className = "admin-btn admin-btn-primary";
      cordonBtn.onclick = () => toggleCordonNode(nodeName, false);
    } else {
      cordonBtn.textContent = "Cordon Node (Mark Unschedulable)";
      cordonBtn.className = "admin-btn admin-btn-danger";
      cordonBtn.onclick = () => toggleCordonNode(nodeName, true);
    }

    $("#drain-modal-body").innerHTML = `
      <div style="background: #f8fafc; border: 1px solid var(--admin-border); border-radius: 8px; padding: 16px; margin-bottom: 16px;">
        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px;">
          <div>
            <div style="font-size: 11px; text-transform: uppercase; font-weight: 700; color: var(--admin-text-muted);">Cluster Headroom Absorption</div>
            <strong style="font-size: 15px; color: ${sim.cluster_headroom.can_absorb_workloads ? "var(--admin-green)" : "var(--admin-red)"};">
              ${sim.cluster_headroom.can_absorb_workloads ? "✓ Surviving Nodes Can Absorb Workloads" : "⚠️ Insufficient Headroom: Pods Will Pending"}
            </strong>
          </div>
          <span class="admin-badge ${sim.unschedulable ? "admin-badge-warning" : "admin-badge-ready"}">
            ${sim.unschedulable ? "Currently Cordoned" : "Active & Schedulable"}
          </span>
        </div>

        <div style="display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; font-size: 12px;">
          <div>
            <span style="color: var(--admin-text-muted); display: block;">Surviving Ready Nodes:</span>
            <strong>${sim.cluster_headroom.available_nodes}</strong>
          </div>
          <div>
            <span style="color: var(--admin-text-muted); display: block;">Available Headroom (CPU/Mem):</span>
            <strong>${sim.cluster_headroom.headroom_cpu} / ${sim.cluster_headroom.headroom_memory}</strong>
          </div>
          <div>
            <span style="color: var(--admin-text-muted); display: block;">Required Eviction (CPU/Mem):</span>
            <strong style="color: var(--admin-accent);">${sim.cluster_headroom.required_evict_cpu} / ${sim.cluster_headroom.required_evict_memory}</strong>
          </div>
          <div>
            <span style="color: var(--admin-text-muted); display: block;">Evictable Pods:</span>
            <strong>${sim.total_evictable_pods} pods (${sim.singleton_pod_count} singleton)</strong>
          </div>
        </div>
      </div>

      ${sim.safety_recommendations && sim.safety_recommendations.length > 0 ? `
        <div style="background: #fff7ed; border: 1px solid #fed7aa; border-radius: 8px; padding: 12px 16px; margin-bottom: 16px;">
          <strong style="font-size: 12px; color: #9a3412;">Safety Recommendations:</strong>
          <ul style="margin: 6px 0 0; padding-left: 18px; font-size: 12px; color: #7c2d12;">
            ${sim.safety_recommendations.map(r => `<li>${escapeHTML(r)}</li>`).join("")}
          </ul>
        </div>
      ` : ""}

      <div style="font-size: 12px; font-weight: 700; margin-bottom: 8px;">Pods to be Evicted (${sim.eviction_candidates.length})</div>
      <div style="max-height: 240px; overflow-y: auto; border: 1px solid var(--admin-border); border-radius: 6px;">
        <table class="admin-table" style="font-size: 11px;">
          <thead>
            <tr>
              <th>Pod Name</th>
              <th>Namespace</th>
              <th>Workload</th>
              <th>Singleton Warning</th>
              <th>CPU Req</th>
              <th>Mem Req</th>
            </tr>
          </thead>
          <tbody>
            ${sim.eviction_candidates.length === 0 ? `<tr><td colspan="6" class="admin-empty-state">No non-DaemonSet pods scheduled on this node.</td></tr>` : ""}
            ${sim.eviction_candidates.map(p => `
              <tr>
                <td class="admin-table-name">${escapeHTML(p.name)}</td>
                <td class="admin-table-ns">${escapeHTML(p.namespace)}</td>
                <td>${escapeHTML(p.workload_kind)}/${escapeHTML(p.workload_name)}</td>
                <td>
                  ${p.is_singleton ? `<span class="admin-badge admin-badge-danger">Singleton (Downtime Risk)</span>` : `<span class="admin-badge admin-badge-ready">Replicated</span>`}
                </td>
                <td style="font-family: var(--admin-mono);">${escapeHTML(p.cpu_request)}</td>
                <td style="font-family: var(--admin-mono);">${escapeHTML(p.memory_request)}</td>
              </tr>
            `).join("")}
          </tbody>
        </table>
      </div>
    `;
  } catch (err) {
    $("#drain-modal-body").innerHTML = `<div class="admin-empty-state" style="color: var(--admin-red);">Simulation failed: ${escapeHTML(err.message)}</div>`;
  }
}

async function toggleCordonNode(nodeName, unschedulable) {
  const btn = $("#drain-modal-cordon-btn");
  btn.disabled = true;
  const endpoint = unschedulable ? `/api/v1/admin/nodes/${encodeURIComponent(nodeName)}/cordon` : `/api/v1/admin/nodes/${encodeURIComponent(nodeName)}/uncordon`;

  try {
    await request(endpoint, { method: "POST" });
    $("#drain-modal-status").innerHTML = `<strong style="color: var(--admin-green);">✓ Node ${escapeHTML(nodeName)} ${unschedulable ? "cordoned" : "uncordoned"} successfully!</strong>`;
    setTimeout(() => {
      openNodeDrainModal(nodeName);
      if (state.activeView === "nodes") renderNodes($("#admin-content-area"));
    }, 1000);
  } catch (err) {
    btn.disabled = false;
    $("#drain-modal-status").innerHTML = `<span style="color: var(--admin-red);">Action failed: ${escapeHTML(err.message)}</span>`;
  }
}

// -------------------------------------------------------------
// Day-2 Superpower 4: Incident Webhook Alert Dispatcher
// -------------------------------------------------------------

async function renderWebhooks(container) {
  const webhooks = await request("/api/v1/admin/webhooks");

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Incident Webhook Alerts</h1>
        <span class="admin-view-count">${webhooks.length}</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-primary" onclick="openWebhookModal()" type="button">
          + Register Webhook Target
        </button>
      </div>
    </div>

    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Destination Name</th>
            <th>Provider Format</th>
            <th>Webhook Endpoint</th>
            <th>Trigger Events</th>
            <th>Created</th>
            <th style="text-align: right;">Actions</th>
          </tr>
        </thead>
        <tbody>
          ${webhooks.length === 0 ? `<tr><td colspan="6" class="admin-empty-state">No webhooks registered. Register a Slack, Discord, or generic destination to dispatch real-time outage alerts.</td></tr>` : ""}
          ${webhooks.map(w => {
            let providerBadge = `<span class="admin-badge admin-badge-info">${escapeHTML(w.provider.toUpperCase())}</span>`;
            if (w.provider === "slack") providerBadge = `<span class="admin-badge" style="background:#e0e7ff;color:#3730a3;border:1px solid #c7d2fe;">SLACK</span>`;
            if (w.provider === "discord") providerBadge = `<span class="admin-badge" style="background:#ede9fe;color:#5b21b6;border:1px solid #ddd6fe;">DISCORD</span>`;

            return `
              <tr>
                <td class="admin-table-name"><strong>${escapeHTML(w.name)}</strong></td>
                <td>${providerBadge}</td>
                <td class="admin-table-ns" style="max-width: 250px; overflow: hidden; text-overflow: ellipsis;" title="${escapeHTML(w.url)}">
                  ${escapeHTML(w.url)}
                </td>
                <td style="font-size: 11px;">${escapeHTML(w.events)}</td>
                <td class="admin-table-age">${escapeHTML(w.created_at)}</td>
                <td>
                  <div class="admin-row-actions" style="justify-content: flex-end;">
                    <button class="admin-action-btn" onclick="testWebhook(${w.id})" title="Dispatch Test Alert">Test Alert</button>
                    <button class="admin-action-btn btn-delete" onclick="deleteWebhook(${w.id})" title="Delete Webhook">Delete</button>
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

function openWebhookModal() {
  $("#webhook-name-input").value = "";
  $("#webhook-url-input").value = "";
  $("#webhook-provider-select").value = "slack";
  $("#webhook-events-input").value = "critical, pod_crash, node_pressure, cert_expiring";
  $("#admin-webhook-modal").classList.add("open");
}

async function handleCreateWebhook(e) {
  e.preventDefault();
  const name = $("#webhook-name-input").value.trim();
  const url = $("#webhook-url-input").value.trim();
  const provider = $("#webhook-provider-select").value;
  const events = $("#webhook-events-input").value.trim();

  try {
    await request("/api/v1/admin/webhooks", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, url, provider, events }),
    });
    $("#admin-webhook-modal").classList.remove("open");
    renderWebhooks($("#admin-content-area"));
  } catch (err) {
    alert("Could not register webhook: " + err.message);
  }
}

async function deleteWebhook(id) {
  if (!confirm("Are you sure you want to delete this incident webhook target?")) return;
  try {
    await request(`/api/v1/admin/webhooks/${id}`, { method: "DELETE" });
    renderWebhooks($("#admin-content-area"));
  } catch (err) {
    alert("Delete failed: " + err.message);
  }
}

async function testWebhook(id) {
  try {
    const res = await request(`/api/v1/admin/webhooks/${id}/test`, { method: "POST" });
    if (res.success) {
      alert(`✓ Test alert delivered successfully! (HTTP ${res.status_code})`);
    } else {
      alert(`⚠️ Test alert failed: ${res.message}`);
    }
  } catch (err) {
    alert("Test dispatch failed: " + err.message);
  }
}

// -------------------------------------------------------------
// Day-2 Superpower 5: Broken Reference & Dangling Config Scanner
// -------------------------------------------------------------

async function renderDanglingConfigs(container) {
  const report = await request("/api/v1/admin/security/dangling-configs");
  const filtered = filterRows(report.issues || [], ["workload_name", "namespace", "missing_name", "missing_kind"]);

  const badge = $("#badge-dangling");
  if (badge) {
    badge.textContent = report.broken_reference_count;
    badge.hidden = report.broken_reference_count === 0;
  }

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Broken Reference & Dangling Config Pre-Flight Scanner</h1>
        <span class="admin-view-count">${report.broken_reference_count}</span>
      </div>
    </div>

    <!-- Overview Hero -->
    <div style="background: ${report.broken_reference_count === 0 ? "#f0fdf4" : "#fef2f2"}; border: 1px solid ${report.broken_reference_count === 0 ? "#bbf7d0" : "#fecaca"}; border-radius: 8px; padding: 16px; margin-bottom: 20px;">
      <div style="display: flex; justify-content: space-between; align-items: center;">
        <div>
          <h3 style="margin: 0 0 4px; font-size: 15px; color: ${report.broken_reference_count === 0 ? "#166534" : "#991b1b"};">
            ${report.broken_reference_count === 0 ? "✓ Zero Dangling Configuration References Detected" : `⚠️ ${report.broken_reference_count} Broken Reference(s) Causing CreateContainerConfigError`}
          </h3>
          <p style="margin: 0; font-size: 13px; color: ${report.broken_reference_count === 0 ? "#15803d" : "#b91c1c"};">
            ${report.broken_reference_count === 0 ? `Scanned ${report.total_scanned_workloads} workloads across all namespaces. All referenced ConfigMaps, Secrets, keys, and volumes exist.` : `Found missing objects or keys referenced in env/envFrom/volumes. Workloads will fail on restart.`}
          </p>
        </div>
        <span class="admin-badge ${report.broken_reference_count === 0 ? "admin-badge-ready" : "admin-badge-danger"}">
          ${report.total_scanned_workloads} Workloads Scanned
        </span>
      </div>
    </div>

    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Workload</th>
            <th>Namespace</th>
            <th>Container</th>
            <th>Reference Type</th>
            <th>Missing Resource</th>
            <th>Missing Key</th>
            <th>Remediation Runbook</th>
          </tr>
        </thead>
        <tbody>
          ${filtered.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No dangling or missing configuration references detected! All workloads are clean.</td></tr>` : ""}
          ${filtered.map(issue => `
            <tr>
              <td class="admin-table-name">
                <span class="admin-kind-badge">${escapeHTML(issue.workload_kind)}</span>
                <strong>${escapeHTML(issue.workload_name)}</strong>
              </td>
              <td class="admin-table-ns">${escapeHTML(issue.namespace)}</td>
              <td><code>${escapeHTML(issue.container)}</code></td>
              <td><span class="admin-badge" style="font-size: 10px;">${escapeHTML(issue.ref_type)}</span></td>
              <td>
                <span class="admin-badge admin-badge-danger">${escapeHTML(issue.missing_kind)}</span>
                <strong style="color: var(--admin-red);">${escapeHTML(issue.missing_name)}</strong>
              </td>
              <td style="font-family: var(--admin-mono);">${issue.missing_key ? escapeHTML(issue.missing_key) : `<span style="color: var(--admin-text-muted);">(entire object missing)</span>`}</td>
              <td style="font-size: 11px; color: var(--admin-text-muted); max-width: 320px;">${escapeHTML(issue.remediation)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>
  `;
}

// -------------------------------------------------------------
// Next-Gen Capability 1: In-Browser Ephemeral Debug Containers
// -------------------------------------------------------------

function openEphemeralDebugModal(namespace, podName) {
  const modal = $("#admin-debug-modal");
  if (!modal) return;
  $("#debug-target-pod").value = `${namespace}/${podName}`;
  $("#debug-target-container").value = "";
  $("#debug-image-select").value = "nicolaka/netshoot";
  $("#debug-custom-image-wrap").hidden = true;
  $("#debug-custom-image-input").value = "";
  modal.dataset.namespace = namespace;
  modal.dataset.pod = podName;
  modal.classList.add("open");
}

function closeEphemeralDebugModal() {
  $("#admin-debug-modal")?.classList.remove("open");
}

async function handleLaunchDebugContainer(e) {
  e.preventDefault();
  const modal = $("#admin-debug-modal");
  const ns = modal.dataset.namespace;
  const pod = modal.dataset.pod;
  const selectVal = $("#debug-image-select").value;
  let image = selectVal;
  if (selectVal === "custom") {
    image = $("#debug-custom-image-input").value.trim();
    if (!image) {
      alert("Please provide a valid custom image URI.");
      return;
    }
  }
  const targetContainer = $("#debug-target-container").value.trim();
  const launchBtn = $("#debug-modal-launch-btn");
  launchBtn.disabled = true;
  launchBtn.textContent = "Injecting Debug Container…";

  try {
    const resp = await request(`/api/v1/admin/pods/${encodeURIComponent(ns)}/${encodeURIComponent(pod)}/debug`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        image: image,
        target_container: targetContainer,
      }),
    });
    closeEphemeralDebugModal();
    // Directly attach interactive terminal to the newly injected container
    openPodTerminal(ns, resp.pod_name, resp.container_name);
  } catch (err) {
    alert("Debug container injection failed: " + err.message);
  } finally {
    launchBtn.disabled = false;
    launchBtn.textContent = "Launch Debug Shell";
  }
}

// -------------------------------------------------------------
// Next-Gen Capability 2: Live Event Storm & Velocity Radar
// -------------------------------------------------------------

async function renderEventStorm(container) {
  let storm;
  try {
    storm = await request("/api/v1/admin/events/storm");
  } catch (err) {
    if (err.message && err.message.includes("No Kubernetes cluster is connected")) {
      container.innerHTML = `
        <div class="admin-view-header"><div class="admin-view-title"><h1>Event Storm Radar</h1></div></div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 16px;">
            <h3>No Kubernetes Cluster Connected</h3>
            <p style="color: var(--admin-text-muted); margin: 8px auto 16px;">Connect a cluster to monitor cluster event storms and velocity anomalies.</p>
            <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect Cluster</a>
          </div>
        </div>`;
      return;
    }
    throw err;
  }

  const clusters = storm.clusters || [];
  let statusBadge = `<span class="admin-storm-badge normal">NORMAL VELOCITY</span>`;
  if (storm.status === "critical") statusBadge = `<span class="admin-storm-badge critical">CRITICAL STORM</span>`;
  else if (storm.status === "warning") statusBadge = `<span class="admin-storm-badge warning">ELEVATED NOISE</span>`;

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Live Event Storm & Anomaly Radar</h1>
        <span class="admin-view-count">${clusters.length} Clusters</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" onclick="renderEventStorm($('#admin-content-area'))">Refresh Radar</button>
      </div>
    </div>

    <!-- Storm Stat Cards -->
    <div class="admin-storm-grid">
      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Status</div>
        <div style="margin: 8px 0;">${statusBadge}</div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">Anomaly Score: <strong>${storm.anomaly_score}/100</strong></div>
      </div>
      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Cluster Velocity</div>
        <div style="font-size: 26px; font-weight: 800; color: ${storm.velocity_per_minute > 20 ? "var(--admin-red)" : "var(--admin-accent)"}; margin: 4px 0;">
          ${storm.velocity_per_minute.toFixed(1)} <span style="font-size: 13px; font-weight: 500; color: var(--admin-text-muted);">events / min</span>
        </div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">Total Ingestion Window: <strong>${storm.total_events} events</strong></div>
      </div>
      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Event Suppression</div>
        <div style="font-size: 26px; font-weight: 800; color: var(--admin-text); margin: 4px 0;">
          ${clusters.length > 0 ? ((1 - clusters.length / Math.max(storm.total_events, 1)) * 100).toFixed(0) : 100}%
        </div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">Noise reduction ratio via root-clustering</div>
      </div>
    </div>

    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Anomaly Score</th>
            <th>Reason & Kind</th>
            <th>Namespace</th>
            <th>Count</th>
            <th>Velocity</th>
            <th>Sample Targets</th>
            <th>Latest Event Signature</th>
            <th style="text-align: right;">Action</th>
          </tr>
        </thead>
        <tbody>
          ${clusters.length === 0 ? `<tr><td colspan="8" class="admin-empty-state">No repetitive event storms or warning clusters detected. Cluster event baseline is calm.</td></tr>` : ""}
          ${clusters.map((c) => {
            let scoreClass = "admin-badge-ready";
            if (c.anomaly_score >= 70) scoreClass = "admin-badge-danger";
            else if (c.anomaly_score >= 35) scoreClass = "admin-badge-warning";

            return `
              <tr>
                <td><span class="admin-badge ${scoreClass}">${c.anomaly_score} / 100</span></td>
                <td>
                  <span class="admin-kind-badge">${escapeHTML(c.kind)}</span>
                  <strong style="color: var(--admin-red); font-size: 12px;">${escapeHTML(c.reason)}</strong>
                </td>
                <td class="admin-table-ns">${escapeHTML(c.namespace)}</td>
                <td style="font-weight: 700;">${c.count}</td>
                <td>
                  <div class="admin-velocity-meter">
                    <span>${c.velocity.toFixed(1)}/m</span>
                    <div class="admin-velocity-bar-wrap">
                      <div class="admin-velocity-bar-fill" style="width: ${Math.min(100, c.velocity * 5)}%;"></div>
                    </div>
                  </div>
                </td>
                <td>
                  <div style="display: flex; gap: 4px; flex-wrap: wrap; max-width: 220px;">
                    ${(c.sample_objects || []).slice(0, 3).map((obj) => `<span class="admin-badge" style="font-size: 10px;">${escapeHTML(obj)}</span>`).join("")}
                  </div>
                </td>
                <td style="font-size: 11px; color: var(--admin-text-muted); max-width: 340px; line-height: 1.4;">
                  ${escapeHTML(c.message)}
                </td>
                <td style="text-align: right;">
                  <a class="admin-btn admin-btn-sm" href="#war-room?ns=${encodeURIComponent(c.namespace)}">War Room</a>
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
// Next-Gen Capability 3: Autoscaling & HPA Flapping Detector
// -------------------------------------------------------------

async function renderAutoscaling(container) {
  let res;
  try {
    res = await request("/api/v1/admin/autoscaling");
  } catch (err) {
    if (err.message && err.message.includes("No Kubernetes cluster is connected")) {
      container.innerHTML = `
        <div class="admin-view-header"><div class="admin-view-title"><h1>Autoscaling & HPA</h1></div></div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 16px;">
            <h3>No Kubernetes Cluster Connected</h3>
            <p style="color: var(--admin-text-muted); margin: 8px auto 16px;">Connect a cluster to inspect HorizontalPodAutoscalers and scaling limits.</p>
            <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect Cluster</a>
          </div>
        </div>`;
      return;
    }
    throw err;
  }

  const hpas = res.hpas || [];

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Autoscaling & HPA Flapping Detector</h1>
        <span class="admin-view-count">${res.total_hpas} HPAs</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" onclick="renderAutoscaling($('#admin-content-area'))">Refresh</button>
      </div>
    </div>

    <!-- HPA KPI Cards -->
    <div class="admin-storm-grid">
      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Managed Autoscalers</div>
        <div style="font-size: 26px; font-weight: 800; color: var(--admin-text); margin: 4px 0;">${res.total_hpas}</div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">Configured HorizontalPodAutoscalers</div>
      </div>
      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">At Scaling Ceiling</div>
        <div style="font-size: 26px; font-weight: 800; color: ${res.at_capacity_count > 0 ? "var(--admin-red)" : "var(--admin-green)"}; margin: 4px 0;">
          ${res.at_capacity_count}
        </div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">Replicas capped at maximum ceiling limit</div>
      </div>
      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Flapping HPAs</div>
        <div style="font-size: 26px; font-weight: 800; color: ${res.flapping_count > 0 ? "var(--admin-amber)" : "var(--admin-green)"}; margin: 4px 0;">
          ${res.flapping_count}
        </div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">Scaling limited or oscillation detected</div>
      </div>
    </div>

    <!-- HPA Cards Grid -->
    ${hpas.length === 0 ? `
      <div class="admin-table-container">
        <div class="admin-empty-state" style="padding: 40px 16px;">
          <h3>No HorizontalPodAutoscalers Found</h3>
          <p style="color: var(--admin-text-muted); max-width: 420px; margin: 8px auto 0;">
            No HPA resources are currently defined in this cluster. Add an HPA to enable automatic horizontal workload scaling.
          </p>
        </div>
      </div>
    ` : `
      <div class="admin-hpa-grid">
        ${hpas.map((h) => {
          let badge = `<span class="admin-badge admin-badge-ready">HEALTHY</span>`;
          let headroomClass = "";
          if (h.status === "at_capacity") {
            badge = `<span class="admin-badge admin-badge-danger">CEILING REACHED</span>`;
            headroomClass = "danger";
          } else if (h.status === "flapping") {
            badge = `<span class="admin-badge admin-badge-warning">FLAPPING / LIMITED</span>`;
            headroomClass = "warning";
          }

          return `
            <div class="admin-hpa-card">
              <div class="admin-hpa-header">
                <div>
                  <div style="font-weight: 700; font-size: 14px; margin-bottom: 2px;">${escapeHTML(h.name)}</div>
                  <div class="admin-table-ns">${escapeHTML(h.namespace)} &bull; ${escapeHTML(h.target_kind)}/${escapeHTML(h.target_name)}</div>
                </div>
                ${badge}
              </div>

              <div>
                <div style="display: flex; justify-content: space-between; font-size: 12px; margin-bottom: 4px;">
                  <span>Replicas: <strong>${h.current_replicas}</strong> (Desired: ${h.desired_replicas})</span>
                  <span style="color: var(--admin-text-muted);">Range: [${h.min_replicas} - ${h.max_replicas}]</span>
                </div>
                <div class="admin-headroom-bar">
                  <div class="admin-headroom-fill ${headroomClass}" style="width: ${Math.max(4, h.headroom_pct)}%;"></div>
                </div>
                <div style="display: flex; justify-content: space-between; font-size: 11px; color: var(--admin-text-muted);">
                  <span>Headroom: ${h.headroom_pct.toFixed(0)}%</span>
                  <span>${h.current_replicas >= h.max_replicas ? "Maxed Out" : `${h.max_replicas - h.current_replicas} Pods Available`}</span>
                </div>
              </div>

              <div>
                <div style="font-size: 11px; font-weight: 600; text-transform: uppercase; color: var(--admin-text-muted); margin-bottom: 4px;">Active Metrics</div>
                <span class="admin-hpa-metric-pill">${escapeHTML(h.metrics_summary || "cpu: target pending")}</span>
              </div>

              ${h.reason ? `
                <div style="background: var(--admin-bg-secondary); padding: 8px 10px; border-radius: 4px; font-size: 11px; color: ${h.status === "at_capacity" ? "var(--admin-red)" : "var(--admin-amber)"}; line-height: 1.4;">
                  ⚠️ ${escapeHTML(h.reason)}
                </div>
              ` : ""}

              <div style="margin-top: auto; display: flex; justify-content: flex-end; gap: 8px;">
                <button class="admin-btn admin-btn-sm" onclick="openScaleModal('${escapeHTML(h.namespace)}', '${escapeHTML(h.target_name)}', ${h.current_replicas})">Adjust Scale</button>
              </div>
            </div>
          `;
        }).join("")}
      </div>
    `}
  `;
}

// -------------------------------------------------------------
// Next-Gen Capability 4: GitOps Drift & Out-of-Band Mutation Radar
// -------------------------------------------------------------

async function renderGitOpsDrift(container) {
  let report;
  try {
    report = await request("/api/v1/admin/gitops/drift");
  } catch (err) {
    if (err.message && err.message.includes("No Kubernetes cluster is connected")) {
      container.innerHTML = `
        <div class="admin-view-header"><div class="admin-view-title"><h1>GitOps Drift Radar</h1></div></div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 16px;">
            <h3>No Kubernetes Cluster Connected</h3>
            <p style="color: var(--admin-text-muted); margin: 8px auto 16px;">Connect a cluster to detect out-of-band mutations and GitOps drift.</p>
            <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect Cluster</a>
          </div>
        </div>`;
      return;
    }
    throw err;
  }

  const drifts = (report.drifts || []).filter((d) => d.has_drift);

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>GitOps Drift & Out-of-Band Mutation Radar</h1>
        <span class="admin-view-count">${report.drift_count} Drifted</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" onclick="renderGitOpsDrift($('#admin-content-area'))">Scan Drift</button>
      </div>
    </div>

    <!-- Drift Banner -->
    <div style="background: ${report.drift_count === 0 ? "var(--admin-green-bg)" : "var(--admin-red-bg)"}; border: 1px solid ${report.drift_count === 0 ? "var(--admin-green-border)" : "var(--admin-red-border)"}; border-radius: 8px; padding: 16px; margin-bottom: 20px;">
      <div style="display: flex; justify-content: space-between; align-items: center;">
        <div>
          <h3 style="margin: 0 0 4px; font-size: 15px; color: ${report.drift_count === 0 ? "var(--admin-green)" : "var(--admin-red)"};">
            ${report.drift_count === 0 ? "✓ All Workloads In Sync with Declared GitOps State" : `⚠️ ${report.drift_count} Out-of-Band Mutation(s) Detected`}
          </h3>
          <p style="margin: 0; font-size: 13px; color: ${report.drift_count === 0 ? "var(--admin-green)" : "var(--admin-red)"};">
            ${report.drift_count === 0 ? `Scanned ${report.total_scanned} managed workloads against 'kubectl.kubernetes.io/last-applied-configuration'. Zero configuration drift.` : `Live cluster state differs from the declarative GitOps specification. Use 1-Click Reconcile to restore intended state.`}
          </p>
        </div>
        <span class="admin-badge ${report.drift_count === 0 ? "admin-badge-ready" : "admin-badge-danger"}">
          ${report.total_scanned} Workloads Scanned
        </span>
      </div>
    </div>

    ${drifts.length === 0 ? `
      <div class="admin-table-container">
        <div class="admin-empty-state" style="padding: 48px 16px;">
          <h3>Cluster Declarative State Intact</h3>
          <p style="color: var(--admin-text-muted); max-width: 440px; margin: 8px auto 0;">
            No out-of-band hotfixes, ad-hoc image mutations, or uncommitted replica modifications detected.
          </p>
        </div>
      </div>
    ` : `
      <div>
        ${drifts.map((d) => `
          <div class="admin-drift-card">
            <div style="display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 10px;">
              <div>
                <span class="admin-kind-badge">${escapeHTML(d.kind)}</span>
                <strong style="font-size: 15px;">${escapeHTML(d.name)}</strong>
                <span class="admin-table-ns" style="margin-left: 8px;">ns: ${escapeHTML(d.namespace)}</span>
              </div>
              <div style="display: flex; gap: 8px; align-items: center;">
                <span class="admin-badge admin-badge-warning" style="text-transform: uppercase;">${escapeHTML(d.drift_type || "mutation")}</span>
                <button class="admin-btn admin-btn-sm admin-btn-primary" onclick="reconcileGitOps('${escapeHTML(d.kind)}', '${escapeHTML(d.namespace)}', '${escapeHTML(d.name)}')">1-Click Reconcile</button>
              </div>
            </div>

            <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-bottom: 10px;">
              <div style="background: var(--admin-bg-secondary); padding: 10px; border-radius: 6px; font-size: 12px;">
                <div style="font-weight: 700; color: var(--admin-text-muted); margin-bottom: 4px;">DECLARED (GitOps)</div>
                <code>${escapeHTML(d.declared_summary)}</code>
              </div>
              <div style="background: var(--admin-bg-secondary); padding: 10px; border-radius: 6px; font-size: 12px;">
                <div style="font-weight: 700; color: var(--admin-red); margin-bottom: 4px;">LIVE MUTATION</div>
                <code>${escapeHTML(d.live_summary)}</code>
              </div>
            </div>

            <div class="admin-diff-box">
              ${(d.diff || "").split("\n").map((line) => {
                let cls = "";
                if (line.startsWith("-")) cls = "del";
                else if (line.startsWith("+")) cls = "add";
                return `<div class="admin-diff-line ${cls}">${escapeHTML(line)}</div>`;
              }).join("")}
            </div>
          </div>
        `).join("")}
      </div>
    `}
  `;
}

async function reconcileGitOps(kind, namespace, name) {
  if (!confirm(`Are you sure you want to reconcile ${kind}/${name} in ${namespace} back to its declared GitOps configuration? This will undo any live out-of-band mutations.`)) {
    return;
  }
  try {
    const res = await request("/api/v1/admin/gitops/reconcile", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ kind, namespace, name }),
    });
    alert(res.message || "Reconciliation complete.");
    renderGitOpsDrift($("#admin-content-area"));
  } catch (err) {
    alert("Reconciliation failed: " + err.message);
  }
}

// -------------------------------------------------------------
// Next-Gen Capability 5: Cluster Disaster Recovery & Backup Radar
// -------------------------------------------------------------

async function renderDisasterRecoveryRadar(container) {
  let dr;
  try {
    dr = await request("/api/v1/admin/dr/radar");
  } catch (err) {
    if (err.message && err.message.includes("No Kubernetes cluster is connected")) {
      container.innerHTML = `
        <div class="admin-view-header"><div class="admin-view-title"><h1>Disaster Recovery Radar</h1></div></div>
        <div class="admin-table-container">
          <div class="admin-empty-state" style="padding: 48px 16px;">
            <h3>No Kubernetes Cluster Connected</h3>
            <p style="color: var(--admin-text-muted); margin: 8px auto 16px;">Connect a cluster to evaluate disaster recovery coverage and PVC backup schedules.</p>
            <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect Cluster</a>
          </div>
        </div>`;
      return;
    }
    throw err;
  }

  const schedules = dr.schedules || [];
  const pvcs = dr.pvc_protection || [];

  let scoreColorClass = "good";
  if (dr.dr_score < 50) scoreColorClass = "poor";
  else if (dr.dr_score < 75) scoreColorClass = "warning";

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Cluster Disaster Recovery & Backup Radar</h1>
        <span class="admin-view-count">Score: ${dr.dr_score}/100</span>
      </div>
      <div class="admin-view-actions">
        <button class="admin-btn admin-btn-sm" onclick="renderDisasterRecoveryRadar($('#admin-content-area'))">Refresh Coverage</button>
      </div>
    </div>

    <!-- DR KPI Row -->
    <div class="admin-dr-stats">
      <div class="admin-storm-card" style="display: flex; align-items: center; gap: 16px;">
        <div class="admin-dr-score-circle ${scoreColorClass}">
          ${dr.dr_score}
          <span style="font-size: 10px; font-weight: 600;">/ 100</span>
        </div>
        <div>
          <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">DR Compliance</div>
          <div style="font-size: 15px; font-weight: 700; text-transform: capitalize; margin: 2px 0;">
            ${dr.compliance_status} Coverage
          </div>
          <div style="font-size: 11px; color: var(--admin-text-muted);">Backup provider: <strong>${escapeHTML(dr.backup_tool || "none")}</strong></div>
        </div>
      </div>

      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Persistent Storage Protection</div>
        <div style="font-size: 26px; font-weight: 800; color: ${dr.unbacked_pvcs > 0 ? "var(--admin-red)" : "var(--admin-green)"}; margin: 4px 0;">
          ${dr.protected_pvcs} / ${dr.total_pvcs}
        </div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">${dr.unbacked_pvcs} unbacked PersistentVolumeClaim(s)</div>
      </div>

      <div class="admin-storm-card">
        <div style="font-size: 11px; text-transform: uppercase; color: var(--admin-text-muted); font-weight: 600;">Backup Schedules</div>
        <div style="font-size: 26px; font-weight: 800; color: var(--admin-text); margin: 4px 0;">${schedules.length}</div>
        <div style="font-size: 12px; color: var(--admin-text-muted);">Automated snapshot / backup cron jobs</div>
      </div>
    </div>

    <!-- Schedules Table -->
    <h2 style="font-size: 14px; margin: 18px 0 8px;">Active Backup Schedules</h2>
    <div class="admin-table-container" style="margin-bottom: 24px;">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Schedule Name</th>
            <th>Cron Expression</th>
            <th>Last Backup</th>
            <th>Status</th>
          </tr>
        </thead>
        <tbody>
          ${schedules.length === 0 ? `<tr><td colspan="4" class="admin-empty-state">No Velero or CSI snapshot schedules detected. Storage volumes lack automated backup policies.</td></tr>` : ""}
          ${schedules.map((s) => `
            <tr>
              <td><strong>${escapeHTML(s.name)}</strong></td>
              <td><code>${escapeHTML(s.schedule)}</code></td>
              <td>${escapeHTML(s.last_backup || "Never")}</td>
              <td><span class="admin-badge admin-badge-ready">${escapeHTML(s.status || "Active")}</span></td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>

    <!-- PVC Protection Table -->
    <h2 style="font-size: 14px; margin: 18px 0 8px;">Persistent Volume Protection & RPO Radar</h2>
    <div class="admin-table-container">
      <table class="admin-table">
        <thead>
          <tr>
            <th>PVC Name</th>
            <th>Namespace</th>
            <th>Capacity</th>
            <th>StorageClass</th>
            <th>Protection Status</th>
            <th>Schedule</th>
            <th>Target RPO</th>
          </tr>
        </thead>
        <tbody>
          ${pvcs.length === 0 ? `<tr><td colspan="7" class="admin-empty-state">No PersistentVolumeClaims detected in cluster.</td></tr>` : ""}
          ${pvcs.map((p) => `
            <tr>
              <td><strong>${escapeHTML(p.name)}</strong></td>
              <td class="admin-table-ns">${escapeHTML(p.namespace)}</td>
              <td>${escapeHTML(p.size)}</td>
              <td class="admin-table-ns">${escapeHTML(p.storage_class || "standard")}</td>
              <td>
                <span class="admin-badge ${p.is_protected ? "admin-badge-ready" : "admin-badge-danger"}">
                  ${p.is_protected ? "PROTECTED" : "UNBACKED"}
                </span>
              </td>
              <td>${p.backup_schedule ? `<code>${escapeHTML(p.backup_schedule)}</code>` : `<span style="color: var(--admin-text-muted); font-size: 11px;">None</span>`}</td>
              <td style="font-weight: 600;">${p.rpo_hours > 0 ? `&le; ${p.rpo_hours}h` : `<span style="color: var(--admin-red);">N/A (At Risk)</span>`}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>
  `;
}

// =============================================================
// Enterprise Suite: Core Capabilities for Crelan Platform
// =============================================================

// Helper: Lightweight YAML Formatter for objects
function objectToYAML(obj, indent = 0) {
  if (obj === null || obj === undefined) return "null\n";
  if (typeof obj === "string") {
    if (obj.includes("\n")) {
      return "|\n" + obj.split("\n").map(line => "  ".repeat(indent + 1) + line).join("\n") + "\n";
    }
    return (obj.includes(":") || obj.includes("#") || obj.startsWith("-")) ? JSON.stringify(obj) + "\n" : obj + "\n";
  }
  if (typeof obj === "number" || typeof obj === "boolean") return String(obj) + "\n";
  const pad = "  ".repeat(indent);
  if (Array.isArray(obj)) {
    if (obj.length === 0) return "[]\n";
    return obj.map(item => {
      if (typeof item === "object" && item !== null) {
        const sub = objectToYAML(item, indent + 1);
        return `${pad}- ${sub.trimStart()}`;
      }
      return `${pad}- ${objectToYAML(item, 0)}`;
    }).join("");
  }
  const keys = Object.keys(obj);
  if (keys.length === 0) return "{}\n";
  return keys.map(k => {
    const val = obj[k];
    if (typeof val === "object" && val !== null && !Array.isArray(val) && Object.keys(val).length > 0) {
      return `${pad}${k}:\n${objectToYAML(val, indent + 1)}`;
    } else if (Array.isArray(val)) {
      if (val.length === 0) return `${pad}${k}: []\n`;
      return `${pad}${k}:\n${objectToYAML(val, indent + 1)}`;
    }
    return `${pad}${k}: ${objectToYAML(val, 0)}`;
  }).join("");
}

// -------------------------------------------------------------
// Container Image Vulnerability Scanner (CVE Radar)
// -------------------------------------------------------------
let currentCVEImages = [];

async function renderImageVulnerabilities(container) {
  container.innerHTML = `<div class="admin-empty-state">Scanning active cluster workloads for vulnerabilities…</div>`;
  const data = await request("/api/v1/admin/security/image-vulnerabilities");
  currentCVEImages = data.images || [];
  const summary = data.summary || {
    total_images: 0,
    scanned_images: 0,
    critical_count: 0,
    high_count: 0,
    medium_count: 0,
    low_count: 0,
    clean_images: 0,
  };

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Container Image Vulnerability Scanner (CVE Radar)</h1>
        <p class="admin-view-subtitle">Real-time vulnerability scanner across all pod images with severity scoring, package impact, and remediation guidance.</p>
      </div>
      <div style="display: flex; gap: 8px;">
        <button class="admin-btn" onclick="openCommandPalette('cve')">⌘K Quick Scan</button>
        <button class="admin-btn admin-btn-primary" onclick="renderImageVulnerabilities($('#admin-content-area'))">Re-scan Cluster Images</button>
      </div>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Images Evaluated</span><span>Workloads</span></div>
        <div class="admin-stat-value">${summary.scanned_images} / ${summary.total_images}</div>
        <div class="admin-stat-subtext">${summary.clean_images} clean without known vulnerabilities</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Critical CVEs</span><span class="cve-badge-critical">CVSS 9.0+</span></div>
        <div class="admin-stat-value" style="color: ${summary.critical_count > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${summary.critical_count}</div>
        <div class="admin-stat-subtext">Immediate remote code execution / root exploits</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>High Severity CVEs</span><span class="cve-badge-high">CVSS 7.0-8.9</span></div>
        <div class="admin-stat-value" style="color: ${summary.high_count > 0 ? "var(--admin-amber)" : "var(--admin-green)"};">${summary.high_count}</div>
        <div class="admin-stat-subtext">Privilege escalation &amp; authentication bypass</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Medium &amp; Low CVEs</span><span class="cve-badge-medium">CVSS &lt; 7.0</span></div>
        <div class="admin-stat-value">${summary.medium_count + summary.low_count}</div>
        <div class="admin-stat-subtext">${summary.medium_count} medium, ${summary.low_count} low severity</div>
      </div>
    </div>

    <div class="admin-filter-bar" style="margin-top: 24px; display: flex; justify-content: space-between; align-items: center;">
      <div style="display: flex; gap: 10px; align-items: center;">
        <input type="text" id="cve-search-input" class="admin-input" placeholder="Filter by image name or workload..." style="width: 320px;" oninput="filterCVETable()">
        <span style="font-size: 12px; color: var(--admin-text-muted);" id="cve-table-count">${currentCVEImages.length} images evaluated</span>
      </div>
    </div>

    <div class="admin-table-container" style="margin-top: 12px;">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Container Image</th>
            <th>Workload Context</th>
            <th>Vulnerabilities Breakdown</th>
            <th>Peak CVSS</th>
            <th style="text-align: right;">Action</th>
          </tr>
        </thead>
        <tbody id="cve-table-body">
          ${renderCVETableRows(currentCVEImages)}
        </tbody>
      </table>
    </div>
  `;
}

function filterCVETable() {
  const query = ($("#cve-search-input")?.value || "").toLowerCase().trim();
  const filtered = currentCVEImages.filter(img => {
    if (img.image.toLowerCase().includes(query)) return true;
    return (img.workloads || []).some(w => `${w.kind}/${w.name} ${w.namespace}`.toLowerCase().includes(query));
  });
  const tbody = $("#cve-table-body");
  if (tbody) tbody.innerHTML = renderCVETableRows(filtered);
  const countSpan = $("#cve-table-count");
  if (countSpan) countSpan.textContent = `${filtered.length} of ${currentCVEImages.length} images`;
}

function renderCVETableRows(images) {
  if (!images || images.length === 0) {
    return `<tr><td colspan="5" class="admin-empty-state">No matching container images running in cluster workloads.</td></tr>`;
  }
  return images.map((img, idx) => {
    const workloadsHtml = (img.workloads || []).slice(0, 3).map(w =>
      `<span class="admin-badge" style="font-size: 11px; margin-right: 4px; margin-bottom: 2px;">${escapeHTML(w.kind)}/${escapeHTML(w.name)} <span style="opacity: 0.6;">(${escapeHTML(w.namespace)})</span></span>`
    ).join("") + ((img.workloads || []).length > 3 ? `<span style="font-size: 11px; color: var(--admin-text-muted);">+${img.workloads.length - 3} more</span>` : "");

    let highestScore = 0;
    (img.vulnerabilities || []).forEach(v => {
      if (v.cvss_score > highestScore) highestScore = v.cvss_score;
    });

    const isClean = (img.total_cves || 0) === 0;

    return `
      <tr>
        <td class="admin-table-name">
          <div style="display: flex; align-items: center; gap: 8px;">
            <span style="font-size: 15px;">${isClean ? "🛡️" : "⚠️"}</span>
            <div>
              <strong style="font-family: monospace; font-size: 13px;">${escapeHTML(img.image)}</strong>
            </div>
          </div>
        </td>
        <td>${workloadsHtml || `<span style="color: var(--admin-text-muted); font-size: 12px;">Active Pod</span>`}</td>
        <td>
          <div style="display: flex; gap: 4px; align-items: center; flex-wrap: wrap;">
            ${img.critical_count > 0 ? `<span class="cve-badge-critical">${img.critical_count} CRITICAL</span>` : ""}
            ${img.high_count > 0 ? `<span class="cve-badge-high">${img.high_count} HIGH</span>` : ""}
            ${img.medium_count > 0 ? `<span class="cve-badge-medium">${img.medium_count} MED</span>` : ""}
            ${img.low_count > 0 ? `<span class="cve-badge-low">${img.low_count} LOW</span>` : ""}
            ${isClean ? `<span class="cve-badge-clean">CLEAN (0 CVEs)</span>` : ""}
          </div>
        </td>
        <td>
          ${highestScore > 0 ? `<span class="admin-badge ${highestScore >= 9.0 ? "admin-badge-danger" : highestScore >= 7.0 ? "admin-badge-warning" : "admin-badge-info"}">${highestScore.toFixed(1)}</span>` : `<span style="color: var(--admin-text-muted); font-size: 12px;">None</span>`}
        </td>
        <td style="text-align: right;">
          ${!isClean ? `<button class="admin-btn admin-btn-sm admin-btn-primary" onclick="openCVEModalByIndex(${idx})">Inspect CVEs (${img.total_cves})</button>` : `<span style="color: var(--admin-green); font-size: 12px; font-weight: 500;">✓ Clean Image</span>`}
        </td>
      </tr>
    `;
  }).join("");
}

function openCVEModalByIndex(idx) {
  const item = currentCVEImages[idx];
  if (!item) return;
  const modal = $("#admin-cve-modal");
  $("#cve-modal-title").textContent = item.image;
  const badge = $("#cve-modal-badge");
  if (item.critical_count > 0) {
    badge.className = "admin-badge admin-badge-danger";
    badge.textContent = `${item.critical_count} CRITICAL`;
  } else if (item.high_count > 0) {
    badge.className = "admin-badge admin-badge-warning";
    badge.textContent = `${item.high_count} HIGH`;
  } else {
    badge.className = "admin-badge admin-badge-info";
    badge.textContent = `${item.total_cves} CVEs`;
  }

  const listHtml = (item.vulnerabilities || []).map(v => `
    <div style="border: 1px solid var(--admin-border); border-radius: var(--admin-radius-sm); padding: 14px; margin-bottom: 12px; background: var(--admin-bg-secondary);">
      <div style="display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 8px;">
        <div>
          <span class="cve-badge-${(v.severity || "low").toLowerCase()}" style="margin-right: 8px;">${escapeHTML(v.severity)}</span>
          <strong style="font-size: 14px; font-family: monospace;">${escapeHTML(v.cve_id)}</strong>
          ${v.cvss_score ? `<span style="margin-left: 8px; font-size: 12px; color: var(--admin-text-muted);">CVSS: <strong>${v.cvss_score}</strong></span>` : ""}
        </div>
        <div style="font-size: 12px; font-family: monospace; color: var(--admin-text-muted);">
          pkg: <strong>${escapeHTML(v.package_name)}</strong> (${escapeHTML(v.installed_version)})
        </div>
      </div>
      <div style="font-weight: 600; font-size: 13px; margin-bottom: 4px;">${escapeHTML(v.title)}</div>
      <p style="font-size: 12px; color: var(--admin-text); line-height: 1.5; margin: 0 0 10px;">${escapeHTML(v.description)}</p>
      <div style="background: rgba(50, 108, 229, 0.08); border-left: 3px solid var(--admin-accent); padding: 8px 12px; font-size: 12px; border-radius: 0 var(--admin-radius-sm) var(--admin-radius-sm) 0;">
        <strong>Remediation:</strong> ${escapeHTML(v.remediation)} ${v.fixed_version ? `(Fixed in version <code style="font-size: 11px;">${escapeHTML(v.fixed_version)}</code>)` : ""}
      </div>
    </div>
  `).join("");

  $("#cve-modal-body").innerHTML = listHtml;
  modal.classList.add("open");
}

// -------------------------------------------------------------
// Helm Release Lifecycle Manager
// -------------------------------------------------------------
let currentHelmReleases = [];
let currentHelmValues = null;
let activeHelmTab = "user";

async function renderHelmReleases(container) {
  container.innerHTML = `<div class="admin-empty-state">Discovering in-cluster Helm releases…</div>`;
  const ns = state.selectedNamespace;
  const url = ns ? `/api/v1/admin/helm/releases?namespace=${encodeURIComponent(ns)}` : `/api/v1/admin/helm/releases`;
  currentHelmReleases = await request(url);

  const deployedCount = currentHelmReleases.filter(r => (r.status || "").toLowerCase() === "deployed").length;
  const failedCount = currentHelmReleases.filter(r => (r.status || "").toLowerCase().includes("fail")).length;

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Helm Releases &amp; Lifecycle Manager</h1>
        <p class="admin-view-subtitle">Lens Pro-tier discovery of Helm v3 releases, values inspection (values.yaml), rollback, and uninstallation.</p>
      </div>
      <div style="display: flex; gap: 8px;">
        <button class="admin-btn" onclick="openCommandPalette('helm')">⌘K Search Helm</button>
        <button class="admin-btn admin-btn-primary" onclick="renderHelmReleases($('#admin-content-area'))">Refresh Releases</button>
      </div>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Total Releases</span><span>Helm v3</span></div>
        <div class="admin-stat-value">${currentHelmReleases.length}</div>
        <div class="admin-stat-subtext">${ns ? `In namespace ${ns}` : "Across all cluster namespaces"}</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Deployed (Healthy)</span><span style="color: var(--admin-green);">Active</span></div>
        <div class="admin-stat-value" style="color: var(--admin-green);">${deployedCount}</div>
        <div class="admin-stat-subtext">Successfully applied charts</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Failed / Attention</span><span style="color: var(--admin-red);">Pending</span></div>
        <div class="admin-stat-value" style="color: ${failedCount > 0 ? "var(--admin-red)" : "var(--admin-green)"};">${failedCount}</div>
        <div class="admin-stat-subtext">${failedCount === 0 ? "No broken Helm releases" : "Requires rollback or investigation"}</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Active Scope</span><span>Filter</span></div>
        <div class="admin-stat-value" style="font-size: 18px; text-transform: uppercase;">${escapeHTML(ns || "All Namespaces")}</div>
        <div class="admin-stat-subtext">Change via topbar dropdown</div>
      </div>
    </div>

    <div class="admin-filter-bar" style="margin-top: 24px; display: flex; justify-content: space-between; align-items: center;">
      <div style="display: flex; gap: 10px; align-items: center;">
        <input type="text" id="helm-search-input" class="admin-input" placeholder="Filter by release name, chart, or status..." style="width: 320px;" oninput="filterHelmTable()">
        <span style="font-size: 12px; color: var(--admin-text-muted);" id="helm-table-count">${currentHelmReleases.length} releases discovered</span>
      </div>
    </div>

    <div class="admin-table-container" style="margin-top: 12px;">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Release Name</th>
            <th>Namespace</th>
            <th>Revision</th>
            <th>Chart</th>
            <th>App Version</th>
            <th>Status</th>
            <th>Last Updated</th>
            <th style="text-align: right;">Actions</th>
          </tr>
        </thead>
        <tbody id="helm-table-body">
          ${renderHelmTableRows(currentHelmReleases)}
        </tbody>
      </table>
    </div>
  `;
}

function filterHelmTable() {
  const query = ($("#helm-search-input")?.value || "").toLowerCase().trim();
  const filtered = currentHelmReleases.filter(r =>
    r.name.toLowerCase().includes(query) ||
    r.namespace.toLowerCase().includes(query) ||
    (r.chart || "").toLowerCase().includes(query) ||
    (r.status || "").toLowerCase().includes(query)
  );
  const tbody = $("#helm-table-body");
  if (tbody) tbody.innerHTML = renderHelmTableRows(filtered);
  const countSpan = $("#helm-table-count");
  if (countSpan) countSpan.textContent = `${filtered.length} of ${currentHelmReleases.length} releases`;
}

function renderHelmTableRows(releases) {
  if (!releases || releases.length === 0) {
    return `<tr><td colspan="8" class="admin-empty-state">No Helm v3 releases discovered in the target namespace.</td></tr>`;
  }
  return releases.map(r => {
    const isDeployed = (r.status || "").toLowerCase() === "deployed";
    return `
      <tr>
        <td class="admin-table-name">
          <div style="display: flex; align-items: center; gap: 8px;">
            <span style="font-size: 15px;">⎈</span>
            <strong>${escapeHTML(r.name)}</strong>
          </div>
        </td>
        <td class="admin-table-ns">${escapeHTML(r.namespace)}</td>
        <td><span class="admin-badge" style="background: rgba(50,108,229,0.1); color: var(--admin-accent); font-weight: 600;">rev ${r.revision}</span></td>
        <td style="font-family: monospace; font-size: 12px;">${escapeHTML(r.chart || "—")}</td>
        <td style="font-family: monospace; font-size: 12px;">${escapeHTML(r.app_version || "—")}</td>
        <td>
          <span class="admin-badge ${isDeployed ? "admin-badge-ready" : "admin-badge-danger"}">${escapeHTML(r.status)}</span>
        </td>
        <td style="font-size: 12px; color: var(--admin-text-muted);">${r.updated ? escapeHTML(new Date(r.updated).toLocaleString()) : "—"}</td>
        <td style="text-align: right;">
          <div style="display: inline-flex; gap: 6px;">
            <button class="admin-btn admin-btn-sm" onclick="openHelmValuesModal('${escapeHTML(r.namespace)}', '${escapeHTML(r.name)}')">Values</button>
            <button class="admin-btn admin-btn-sm" onclick="rollbackHelmReleasePrompt('${escapeHTML(r.namespace)}', '${escapeHTML(r.name)}', ${r.revision})">Rollback</button>
            <button class="admin-btn admin-btn-sm admin-btn-danger" onclick="uninstallHelmReleasePrompt('${escapeHTML(r.namespace)}', '${escapeHTML(r.name)}')">Uninstall</button>
          </div>
        </td>
      </tr>
    `;
  }).join("");
}

async function openHelmValuesModal(namespace, name) {
  const modal = $("#admin-helm-modal");
  $("#helm-modal-title").textContent = `${name} (${namespace})`;
  $("#helm-modal-meta").textContent = "Loading values from secret…";
  $("#helm-values-code").textContent = "Fetching values.yaml…";
  modal.classList.add("open");

  try {
    const data = await request(`/api/v1/admin/helm/releases/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/values`);
    currentHelmValues = data;
    $("#helm-modal-meta").textContent = `Chart: ${data.chart} · Revision ${data.revision}`;
    switchHelmValuesTab("user");
  } catch (err) {
    $("#helm-values-code").textContent = `Failed to load Helm values: ${err.message}`;
  }
}

function switchHelmValuesTab(tab) {
  activeHelmTab = tab;
  $("#tab-btn-helm-user")?.classList.toggle("active", tab === "user");
  $("#tab-btn-helm-computed")?.classList.toggle("active", tab === "computed");

  if (!currentHelmValues) return;
  const codeEl = $("#helm-values-code");
  if (tab === "user") {
    codeEl.textContent = currentHelmValues.raw_yaml || objectToYAML(currentHelmValues.user_values || {});
  } else {
    codeEl.textContent = objectToYAML(currentHelmValues.chart_values || {});
  }
}

function copyHelmValuesToClipboard() {
  const text = $("#helm-values-code")?.textContent || "";
  navigator.clipboard.writeText(text).then(() => {
    alert("Helm values copied to clipboard!");
  }).catch(() => {
    alert("Could not copy values to clipboard.");
  });
}

async function rollbackHelmReleasePrompt(namespace, name, currentRev) {
  const targetRevStr = prompt(`Rollback Helm release '${name}' in namespace '${namespace}'\nCurrent revision: ${currentRev}\nEnter target revision number to roll back to:`, currentRev > 1 ? currentRev - 1 : 1);
  if (!targetRevStr) return;
  const targetRev = parseInt(targetRevStr.trim(), 10);
  if (isNaN(targetRev) || targetRev <= 0) {
    alert("Please enter a valid positive integer revision.");
    return;
  }
  try {
    await request(`/api/v1/admin/helm/releases/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/rollback`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ revision: targetRev }),
    });
    alert(`Successfully rolled back release '${name}' to revision ${targetRev}!`);
    await renderHelmReleases($("#admin-content-area"));
  } catch (err) {
    alert(`Rollback failed: ${err.message}`);
  }
}

async function uninstallHelmReleasePrompt(namespace, name) {
  if (!confirm(`Are you sure you want to completely uninstall Helm release '${name}' in namespace '${namespace}'? This will delete all managed resources.`)) {
    return;
  }
  try {
    await request(`/api/v1/admin/helm/releases/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`, {
      method: "DELETE",
    });
    alert(`Successfully uninstalled release '${name}'.`);
    await renderHelmReleases($("#admin-content-area"));
  } catch (err) {
    alert(`Uninstall failed: ${err.message}`);
  }
}

// -------------------------------------------------------------
// Custom Resource Definitions (CRDs) & Operator Browser
// -------------------------------------------------------------
let currentCRDList = [];
let activeCRDInstances = null;

async function renderCRDs(container) {
  container.innerHTML = `<div class="admin-empty-state">Querying Custom Resource Definitions (apiextensions.k8s.io/v1)…</div>`;
  currentCRDList = await request("/api/v1/admin/crds");
  activeCRDInstances = null;

  const totalCRDs = currentCRDList.length;
  const groups = new Set(currentCRDList.map(c => c.group)).size;
  const namespacedCount = currentCRDList.filter(c => c.scope === "Namespaced").length;
  const clusterCount = currentCRDList.filter(c => c.scope === "Cluster").length;

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Custom Resource Definitions (CRDs) &amp; Operator Browser</h1>
        <p class="admin-view-subtitle">Lens Pro-tier CRD exploration, schema discovery, and real-time instance browsing for all installed Kubernetes operators.</p>
      </div>
      <div style="display: flex; gap: 8px;">
        <button class="admin-btn" onclick="openCommandPalette('crd')">⌘K Search CRDs</button>
        <button class="admin-btn admin-btn-primary" onclick="renderCRDs($('#admin-content-area'))">Refresh CRDs</button>
      </div>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Installed CRDs</span><span>Definitions</span></div>
        <div class="admin-stat-value">${totalCRDs}</div>
        <div class="admin-stat-subtext">Active custom resource schemas</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>API Groups</span><span>Vendors/Operators</span></div>
        <div class="admin-stat-value">${groups}</div>
        <div class="admin-stat-subtext">Unique API groups registered</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Namespaced CRDs</span><span>Multi-Tenant</span></div>
        <div class="admin-stat-value" style="color: var(--admin-accent);">${namespacedCount}</div>
        <div class="admin-stat-subtext">Scoped to individual namespaces</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Cluster Scoped</span><span>Global</span></div>
        <div class="admin-stat-value">${clusterCount}</div>
        <div class="admin-stat-subtext">Cluster-wide custom resources</div>
      </div>
    </div>

    <div id="crd-instance-container"></div>

    <div class="admin-filter-bar" style="margin-top: 24px; display: flex; justify-content: space-between; align-items: center;">
      <div style="display: flex; gap: 10px; align-items: center;">
        <input type="text" id="crd-search-input" class="admin-input" placeholder="Filter by kind, group, or resource name..." style="width: 320px;" oninput="filterCRDTable()">
        <span style="font-size: 12px; color: var(--admin-text-muted);" id="crd-table-count">${currentCRDList.length} CRDs registered</span>
      </div>
    </div>

    <div class="admin-table-container" style="margin-top: 12px;">
      <table class="admin-table">
        <thead>
          <tr>
            <th>Custom Resource / Kind</th>
            <th>API Group</th>
            <th>Version</th>
            <th>Scope</th>
            <th>Condition</th>
            <th>Instances</th>
            <th style="text-align: right;">Action</th>
          </tr>
        </thead>
        <tbody id="crd-table-body">
          ${renderCRDTableRows(currentCRDList)}
        </tbody>
      </table>
    </div>
  `;
}

function filterCRDTable() {
  const query = ($("#crd-search-input")?.value || "").toLowerCase().trim();
  const filtered = currentCRDList.filter(c =>
    c.name.toLowerCase().includes(query) ||
    c.kind.toLowerCase().includes(query) ||
    c.group.toLowerCase().includes(query)
  );
  const tbody = $("#crd-table-body");
  if (tbody) tbody.innerHTML = renderCRDTableRows(filtered);
  const countSpan = $("#crd-table-count");
  if (countSpan) countSpan.textContent = `${filtered.length} of ${currentCRDList.length} CRDs`;
}

function renderCRDTableRows(crds) {
  if (!crds || crds.length === 0) {
    return `<tr><td colspan="7" class="admin-empty-state">No matching Custom Resource Definitions found.</td></tr>`;
  }
  return crds.map(c => `
    <tr>
      <td class="admin-table-name">
        <div style="display: flex; align-items: center; gap: 8px;">
          <span style="font-size: 15px;">🧩</span>
          <div>
            <strong>${escapeHTML(c.kind)}</strong>
            <div style="font-size: 11px; font-family: monospace; color: var(--admin-text-muted);">${escapeHTML(c.plural)}</div>
          </div>
        </div>
      </td>
      <td style="font-family: monospace; font-size: 12px; color: var(--admin-accent);">${escapeHTML(c.group)}</td>
      <td><span class="admin-badge" style="font-size: 11px;">${escapeHTML(c.version)}</span></td>
      <td>
        <span class="admin-badge ${c.scope === "Namespaced" ? "admin-badge-info" : "admin-badge-warning"}">${escapeHTML(c.scope)}</span>
      </td>
      <td>
        <span class="admin-badge ${c.established ? "admin-badge-ready" : "admin-badge-danger"}">${c.established ? "Established" : "Not Ready"}</span>
      </td>
      <td style="font-weight: 600;">${c.instances_count !== undefined ? c.instances_count : "—"}</td>
      <td style="text-align: right;">
        <button class="admin-btn admin-btn-sm admin-btn-primary" onclick="browseCRDInstances('${escapeHTML(c.group)}', '${escapeHTML(c.version)}', '${escapeHTML(c.plural)}', '${escapeHTML(c.kind)}')">Browse Instances</button>
      </td>
    </tr>
  `).join("");
}

async function browseCRDInstances(group, version, plural, kind) {
  const container = $("#crd-instance-container");
  if (!container) return;
  container.innerHTML = `
    <div style="margin-top: 24px; padding: 16px; border: 1px solid var(--admin-accent); border-radius: var(--admin-radius-sm); background: rgba(50,108,229,0.03);">
      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px;">
        <h3 style="margin: 0; font-size: 15px;">Live Custom Resources: <span style="color: var(--admin-accent);">${escapeHTML(kind)}</span> <code style="font-size: 12px; opacity: 0.8;">(${escapeHTML(group)}/${escapeHTML(version)})</code></h3>
        <button class="admin-btn admin-btn-sm" onclick="$('#crd-instance-container').innerHTML=''">Close Panel</button>
      </div>
      <div class="admin-empty-state">Loading instances…</div>
    </div>
  `;
  container.scrollIntoView({ behavior: "smooth", block: "start" });

  try {
    const ns = state.selectedNamespace;
    const url = ns ? `/api/v1/admin/crds/${encodeURIComponent(group)}/${encodeURIComponent(version)}/${encodeURIComponent(plural)}?namespace=${encodeURIComponent(ns)}` : `/api/v1/admin/crds/${encodeURIComponent(group)}/${encodeURIComponent(version)}/${encodeURIComponent(plural)}`;
    const data = await request(url);
    activeCRDInstances = data.items || [];

    if (activeCRDInstances.length === 0) {
      container.innerHTML = `
        <div style="margin-top: 24px; padding: 16px; border: 1px solid var(--admin-border); border-radius: var(--admin-radius-sm); background: var(--admin-bg-secondary);">
          <div style="display: flex; justify-content: space-between; align-items: center;">
            <h3 style="margin: 0; font-size: 15px;">No active instances found for <strong>${escapeHTML(kind)}</strong></h3>
            <button class="admin-btn admin-btn-sm" onclick="$('#crd-instance-container').innerHTML=''">Close</button>
          </div>
          <p style="margin: 8px 0 0; font-size: 12px; color: var(--admin-text-muted);">No custom resources exist in ${ns ? `namespace '${ns}'` : "the cluster"}.</p>
        </div>
      `;
      return;
    }

    container.innerHTML = `
      <div style="margin-top: 24px; padding: 16px; border: 1px solid var(--admin-border); border-radius: var(--admin-radius-sm); background: var(--admin-bg);">
        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px;">
          <h3 style="margin: 0; font-size: 15px;">Instances of <strong>${escapeHTML(kind)}</strong> <span style="font-size: 12px; color: var(--admin-text-muted);">(${activeCRDInstances.length} objects)</span></h3>
          <button class="admin-btn admin-btn-sm" onclick="$('#crd-instance-container').innerHTML=''">Close</button>
        </div>
        <div class="admin-table-container">
          <table class="admin-table">
            <thead>
              <tr>
                <th>Resource Name</th>
                <th>Namespace</th>
                <th>Created</th>
                <th style="text-align: right;">Action</th>
              </tr>
            </thead>
            <tbody>
              ${activeCRDInstances.map((item, idx) => `
                <tr>
                  <td class="admin-table-name"><strong>${escapeHTML(item.metadata?.name || "unnamed")}</strong></td>
                  <td class="admin-table-ns">${escapeHTML(item.metadata?.namespace || "Cluster-scoped")}</td>
                  <td style="font-size: 12px; color: var(--admin-text-muted);">${item.metadata?.creationTimestamp ? new Date(item.metadata.creationTimestamp).toLocaleString() : "—"}</td>
                  <td style="text-align: right;">
                    <button class="admin-btn admin-btn-sm" onclick="inspectCRDInstanceYAML(${idx})">View YAML</button>
                  </td>
                </tr>
              `).join("")}
            </tbody>
          </table>
        </div>
      </div>
    `;
  } catch (err) {
    container.innerHTML = `
      <div style="margin-top: 24px; padding: 16px; border: 1px solid var(--admin-red); border-radius: var(--admin-radius-sm); background: var(--admin-bg-secondary);">
        <div style="display: flex; justify-content: space-between; align-items: center;">
          <h3 style="margin: 0; font-size: 15px; color: var(--admin-red);">Failed to query ${escapeHTML(kind)} instances</h3>
          <button class="admin-btn admin-btn-sm" onclick="$('#crd-instance-container').innerHTML=''">Close</button>
        </div>
        <p style="margin: 8px 0 0; font-size: 12px; color: var(--admin-text);">${escapeHTML(err.message)}</p>
      </div>
    `;
  }
}

function inspectCRDInstanceYAML(idx) {
  const item = activeCRDInstances ? activeCRDInstances[idx] : null;
  if (!item) return;
  const modal = $("#admin-helm-modal");
  $("#helm-modal-title").textContent = `${item.kind || "CustomResource"}: ${item.metadata?.name || "object"}`;
  $("#helm-modal-meta").textContent = item.metadata?.namespace ? `Namespace: ${item.metadata.namespace}` : "Cluster-scoped";
  $("#tab-btn-helm-user").textContent = "YAML Definition";
  $("#tab-btn-helm-computed").textContent = "JSON Definition";
  $("#tab-btn-helm-user").classList.add("active");
  $("#tab-btn-helm-computed").classList.remove("active");
  $("#helm-values-code").textContent = objectToYAML(item);

  currentHelmValues = {
    raw_yaml: objectToYAML(item),
    chart_values: item,
  };
  modal.classList.add("open");
}

// -------------------------------------------------------------
// Interactive RBAC Permission Inspector (can-i)
// -------------------------------------------------------------
async function renderCanI(container) {
  const defaultNs = state.selectedNamespace || "default";

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Interactive RBAC Permission Inspector (can-i)</h1>
        <p class="admin-view-subtitle">Lens Pro-tier visual &quot;Who Can Do What&quot; inspector. Evaluate Kubernetes RBAC authorizations across users, groups, and service accounts.</p>
      </div>
      <div style="display: flex; gap: 8px;">
        <button class="admin-btn" onclick="openCommandPalette('can-i')">⌘K Quick Can-I</button>
        <button class="admin-btn admin-btn-primary" onclick="setRoute('rbac')">RBAC Blast Radius</button>
      </div>
    </div>

    <div style="display: grid; grid-template-columns: 1fr 340px; gap: 20px; margin-top: 10px;">
      <div class="admin-card" style="padding: 20px;">
        <h2 style="font-size: 15px; margin-top: 0; margin-bottom: 16px;">Test Authorization Policy</h2>
        <form id="can-i-form" onsubmit="handleCanISubmit(event)">
          <div class="admin-form-group" style="margin-bottom: 14px;">
            <label style="font-weight: 600; font-size: 12px; display: block; margin-bottom: 4px;">Subject / Identity</label>
            <input type="text" id="can-i-subject" class="admin-input" placeholder="e.g. system:serviceaccount:default:deploy-bot or leave blank for current admin user" style="width: 100%;">
            <div style="font-size: 11px; color: var(--admin-text-muted); margin-top: 4px;">Leave blank to evaluate permissions of your active cluster administrator session.</div>
          </div>

          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 14px; margin-bottom: 14px;">
            <div class="admin-form-group">
              <label style="font-weight: 600; font-size: 12px; display: block; margin-bottom: 4px;">API Verb</label>
              <select id="can-i-verb" class="admin-select" style="width: 100%;">
                <option value="get">get</option>
                <option value="list">list</option>
                <option value="watch">watch</option>
                <option value="create" selected>create</option>
                <option value="update">update</option>
                <option value="patch">patch</option>
                <option value="delete">delete</option>
                <option value="deletecollection">deletecollection</option>
                <option value="*">* (All Verbs)</option>
              </select>
            </div>
            <div class="admin-form-group">
              <label style="font-weight: 600; font-size: 12px; display: block; margin-bottom: 4px;">Resource Kind</label>
              <input type="text" id="can-i-resource" class="admin-input" value="deployments" placeholder="e.g. pods, secrets, deployments, *" style="width: 100%;">
            </div>
          </div>

          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 14px; margin-bottom: 20px;">
            <div class="admin-form-group">
              <label style="font-weight: 600; font-size: 12px; display: block; margin-bottom: 4px;">API Group</label>
              <input type="text" id="can-i-group" class="admin-input" value="apps" placeholder="e.g. apps, batch, or blank for core" style="width: 100%;">
            </div>
            <div class="admin-form-group">
              <label style="font-weight: 600; font-size: 12px; display: block; margin-bottom: 4px;">Namespace</label>
              <input type="text" id="can-i-namespace" class="admin-input" value="${escapeHTML(defaultNs)}" placeholder="e.g. default, production, or blank" style="width: 100%;">
            </div>
          </div>

          <button type="submit" class="admin-btn admin-btn-primary" style="width: 100%; padding: 10px; font-weight: 600;" id="can-i-submit-btn">
            Evaluate RBAC Access (can-i)
          </button>
        </form>

        <div id="can-i-result-area" style="margin-top: 20px;"></div>
      </div>

      <div class="admin-card" style="padding: 20px;">
        <h3 style="font-size: 14px; margin-top: 0; margin-bottom: 12px;">Quick RBAC Presets</h3>
        <p style="font-size: 12px; color: var(--admin-text-muted); line-height: 1.5; margin-bottom: 14px;">Click any security probe below to immediately run an RBAC policy evaluation:</p>
        
        <div style="display: flex; flex-direction: column; gap: 8px;">
          <button class="admin-btn admin-btn-sm" style="text-align: left; padding: 8px 10px;" onclick="applyCanIPreset('', 'create', 'deployments', 'apps', 'default')">
            👑 Can Current User create Deployments?
          </button>
          <button class="admin-btn admin-btn-sm" style="text-align: left; padding: 8px 10px;" onclick="applyCanIPreset('system:serviceaccount:default:default', 'get', 'secrets', '', 'default')">
            🔒 Can default SA read Secrets in default?
          </button>
          <button class="admin-btn admin-btn-sm" style="text-align: left; padding: 8px 10px;" onclick="applyCanIPreset('system:anonymous', 'list', 'pods', '', 'default')">
            🕵️ Can Anonymous user list Pods?
          </button>
          <button class="admin-btn admin-btn-sm" style="text-align: left; padding: 8px 10px;" onclick="applyCanIPreset('system:serviceaccount:pipeops:builder', 'create', 'pods', '', 'pipeops')">
            🏗️ Can PipeOps Builder create Pods?
          </button>
          <button class="admin-btn admin-btn-sm" style="text-align: left; padding: 8px 10px;" onclick="applyCanIPreset('developer', 'delete', 'namespaces', '', '')">
            🛑 Can Developer delete Namespaces?
          </button>
        </div>
      </div>
    </div>
  `;
}

function applyCanIPreset(subject, verb, resource, group, namespace) {
  $("#can-i-subject").value = subject;
  $("#can-i-verb").value = verb;
  $("#can-i-resource").value = resource;
  $("#can-i-group").value = group;
  $("#can-i-namespace").value = namespace;
  handleCanISubmit(new Event("submit"));
}

async function handleCanISubmit(e) {
  if (e && e.preventDefault) e.preventDefault();
  const subject = ($("#can-i-subject")?.value || "").trim();
  const verb = ($("#can-i-verb")?.value || "get").trim();
  const resource = ($("#can-i-resource")?.value || "pods").trim();
  const group = ($("#can-i-group")?.value || "").trim();
  const namespace = ($("#can-i-namespace")?.value || "").trim();

  const resultArea = $("#can-i-result-area");
  resultArea.innerHTML = `<div class="admin-empty-state">Evaluating RBAC permissions against cluster API...</div>`;

  try {
    const resp = await request("/api/v1/admin/security/can-i", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ subject, verb, resource, group, namespace }),
    });

    const isAllowed = resp.allowed === true;
    resultArea.innerHTML = `
      <div style="border-radius: var(--admin-radius-sm); border: 2px solid ${isAllowed ? "var(--admin-green)" : "var(--admin-red)"}; background: ${isAllowed ? "rgba(16,185,129,0.06)" : "rgba(239,68,68,0.06)"}; padding: 18px; margin-top: 12px;">
        <div style="display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px;">
          <div style="display: flex; align-items: center; gap: 10px;">
            <span style="font-size: 24px;">${isAllowed ? "✅" : "🚫"}</span>
            <strong style="font-size: 18px; color: ${isAllowed ? "var(--admin-green)" : "var(--admin-red)"}; letter-spacing: 0.5px;">${isAllowed ? "ALLOWED" : "DENIED"}</strong>
          </div>
          <span class="admin-badge" style="font-size: 11px;">${escapeHTML(resp.evaluation_mode || "RBAC Engine")}</span>
        </div>
        <p style="font-size: 13px; line-height: 1.5; color: var(--admin-text); margin: 6px 0 10px;">
          ${escapeHTML(resp.reason || (isAllowed ? "Authorization granted by cluster RBAC policy rules." : "No RoleBinding or ClusterRoleBinding grants this operation."))}
        </p>
        <div style="font-size: 11px; font-family: monospace; color: var(--admin-text-muted); background: var(--admin-bg); padding: 8px; border-radius: var(--admin-radius-sm); border: 1px solid var(--admin-border);">
          Verb: <strong>${escapeHTML(verb)}</strong> · Resource: <strong>${escapeHTML(resource)}</strong> · Group: <strong>${escapeHTML(group || "core")}</strong> · Namespace: <strong>${escapeHTML(namespace || "(cluster-wide)")}</strong>
        </div>
      </div>
    `;
  } catch (err) {
    resultArea.innerHTML = `
      <div style="border-radius: var(--admin-radius-sm); border: 1px solid var(--admin-red); background: var(--admin-bg-secondary); padding: 14px; margin-top: 12px; color: var(--admin-red);">
        <strong>Check Failed:</strong> ${escapeHTML(err.message)}
      </div>
    `;
  }
}

// -------------------------------------------------------------
// Multi-Cluster Fleet Dashboard
// -------------------------------------------------------------
async function renderClusterFleet(container) {
  container.innerHTML = `<div class="admin-empty-state">Loading cluster fleet status…</div>`;
  const fleet = await request("/api/v1/admin/clusters/fleet");

  const totalClusters = fleet.length;
  const currentCluster = fleet.find(c => c.is_current);

  container.innerHTML = `
    <div class="admin-view-header">
      <div class="admin-view-title">
        <h1>Multi-Cluster Fleet Dashboard</h1>
        <p class="admin-view-subtitle">Lens Pro-tier centralized multi-cluster fleet management with instant context switching and live health telemetry.</p>
      </div>
      <div style="display: flex; gap: 8px;">
        <a class="admin-btn admin-btn-primary" href="#cluster-connect">Connect New Cluster</a>
      </div>
    </div>

    <div class="admin-overview-grid">
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Managed Clusters</span><span>Fleet Size</span></div>
        <div class="admin-stat-value">${totalClusters}</div>
        <div class="admin-stat-subtext">Configured cluster environments</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Active Context</span><span style="color: var(--admin-green);">Connected</span></div>
        <div class="admin-stat-value" style="font-size: 20px; font-family: monospace;">${escapeHTML(currentCluster ? currentCluster.name : "None")}</div>
        <div class="admin-stat-subtext">${currentCluster ? currentCluster.server_version : "Disconnected"}</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Active Nodes</span><span>Capacity</span></div>
        <div class="admin-stat-value">${currentCluster ? currentCluster.nodes_count : 0}</div>
        <div class="admin-stat-subtext">Nodes in active context</div>
      </div>
      <div class="admin-stat-card">
        <div class="admin-stat-header"><span>Fleet Health</span><span>Status</span></div>
        <div class="admin-stat-value" style="color: var(--admin-green);">100%</div>
        <div class="admin-stat-subtext">All reachable contexts healthy</div>
      </div>
    </div>

    <h2 style="font-size: 15px; margin: 28px 0 14px;">Available Cluster Contexts</h2>
    <div style="display: grid; grid-template-columns: repeat(auto-fill, minmax(360px, 1fr)); gap: 16px;">
      ${fleet.map(c => `
        <div class="admin-card" style="padding: 20px; border: ${c.is_current ? "2px solid var(--admin-accent)" : "1px solid var(--admin-border)"}; position: relative;">
          ${c.is_current ? `<span class="admin-badge admin-badge-ready" style="position: absolute; top: 16px; right: 16px;">ACTIVE CONTEXT</span>` : ""}
          <div style="display: flex; align-items: center; gap: 10px; margin-bottom: 12px;">
            <span style="font-size: 22px;">☸️</span>
            <div>
              <h3 style="margin: 0; font-size: 16px;">${escapeHTML(c.name)}</h3>
              <span class="fleet-provider-badge">${escapeHTML(c.provider || "Kubernetes")}</span>
            </div>
          </div>

          <div style="font-size: 12px; color: var(--admin-text-muted); margin-bottom: 14px; font-family: monospace; word-break: break-all;">
            ${escapeHTML(c.server || "In-Cluster Endpoint")}
          </div>

          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 10px; padding: 10px; background: var(--admin-bg-secondary); border-radius: var(--admin-radius-sm); margin-bottom: 16px; font-size: 12px;">
            <div>
              <span style="color: var(--admin-text-muted);">Version:</span>
              <strong>${escapeHTML(c.server_version || "—")}</strong>
            </div>
            <div>
              <span style="color: var(--admin-text-muted);">Nodes:</span>
              <strong>${c.nodes_count !== undefined ? c.nodes_count : "—"}</strong>
            </div>
          </div>

          <div>
            ${c.is_current ? `
              <button class="admin-btn admin-btn-sm" style="width: 100%; opacity: 0.7;" disabled>Currently Active</button>
            ` : `
              <button class="admin-btn admin-btn-primary admin-btn-sm" style="width: 100%;" onclick="switchClusterContext('${escapeHTML(c.name)}')">Switch Context to ${escapeHTML(c.name)}</button>
            `}
          </div>
        </div>
      `).join("")}
    </div>
  `;
}

async function switchClusterContext(contextName) {
  try {
    await request("/api/v1/admin/clusters/switch-context", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ context: contextName }),
    });
    alert(`Switched active context to '${contextName}'.`);
    await loadClusterHeader();
    await renderClusterFleet($("#admin-content-area"));
  } catch (err) {
    alert(`Failed to switch context: ${err.message}`);
  }
}

// -------------------------------------------------------------
// Quick Launcher Command Palette (Cmd+K / Ctrl+K)
// -------------------------------------------------------------
const COMMAND_PALETTE_ITEMS = [
  { title: "Container Image CVE Radar", category: "Lens Pro Suite", hash: "image-vulnerabilities", icon: "🛡️", desc: "Scan container images for CVE vulnerabilities and view package remediation" },
  { title: "Helm Releases Manager", category: "Lens Pro Suite", hash: "helm-releases", icon: "⎈", desc: "Discover Helm releases, inspect values.yaml, rollback, and uninstall" },
  { title: "Custom Resource Definitions (CRDs)", category: "Lens Pro Suite", hash: "crds", icon: "🧩", desc: "Browse installed CRDs, schemas, and live custom operator resources" },
  { title: "Interactive RBAC Tester (can-i)", category: "Lens Pro Suite", hash: "can-i", icon: "🔑", desc: "Test RBAC authorization verdicts across users, groups, and service accounts" },
  { title: "Multi-Cluster Fleet Dashboard", category: "Lens Pro Suite", hash: "cluster-fleet", icon: "🌐", desc: "Monitor cluster fleet status and switch Kubernetes contexts" },
  { title: "Cluster Overview", category: "Cluster", hash: "overview", icon: "📊", desc: "CPU/Memory resource gauges, node/pod statistics, and cluster health" },
  { title: "Nodes & Node Health", category: "Cluster", hash: "nodes", icon: "🖥️", desc: "Inspect Kubernetes nodes, taints, capacity, and conditions" },
  { title: "Pods & Containers", category: "Workloads", hash: "pods", icon: "📦", desc: "Explore running pods, container logs, exec terminal, and restarts" },
  { title: "Deployments", category: "Workloads", hash: "deployments", icon: "🚀", desc: "Manage deployments, rollout restarts, and replica scaling" },
  { title: "StatefulSets", category: "Workloads", hash: "statefulsets", icon: "💾", desc: "Stateful workloads, volume claim templates, and status" },
  { title: "DaemonSets", category: "Workloads", hash: "daemonsets", icon: "⚙️", desc: "Per-node daemon workloads and scheduled pods" },
  { title: "Jobs & CronJobs", category: "Workloads", hash: "jobs", icon: "⏱️", desc: "Batch jobs and scheduled cron schedules" },
  { title: "Services & Endpoints", category: "Networking", hash: "services", icon: "🔌", desc: "ClusterIP, NodePort, and LoadBalancer routing" },
  { title: "Ingress Controllers", category: "Networking", hash: "ingresses", icon: "🌍", desc: "HTTP/HTTPS routing rules, hosts, and TLS certificates" },
  { title: "Network Policies", category: "Networking", hash: "networkpolicies", icon: "🧱", desc: "Ingress/egress isolation rules and pod selectors" },
  { title: "ConfigMaps & Secrets", category: "Config & Storage", hash: "configmaps", icon: "🔐", desc: "Cluster configuration maps and encoded secrets" },
  { title: "PersistentVolumeClaims (PVCs)", category: "Config & Storage", hash: "pvcs", icon: "💽", desc: "Storage claims, sizes, and bound PersistentVolumes" },
  { title: "StorageClasses", category: "Config & Storage", hash: "storageclasses", icon: "🏷️", desc: "Dynamic provisioners and volume expansion rules" },
  { title: "Namespaces & Tenant Projects", category: "Cluster", hash: "namespaces", icon: "📁", desc: "Namespace isolation, quotas, and customer tenant bindings" },
  { title: "Cluster Warning Events", category: "Cluster", hash: "events", icon: "⚠️", desc: "Real-time cluster warning event stream and reasons" },
  { title: "AI Diagnostic Copilot", category: "Intelligence", hash: "copilot", icon: "🤖", desc: "Cluster troubleshooting and natural language remediation engine" },
  { title: "Incident War Room", category: "Intelligence", hash: "war-room", icon: "🚨", desc: "Live incident triage, root-cause diagnosis, and post-mortem runbooks" },
  { title: "Right-Sizing & Cost Optimization", category: "Intelligence", hash: "rightsizing", icon: "💡", desc: "Over-provisioned CPU/Memory detection and cost savings" },
  { title: "Disaster Recovery Radar", category: "Intelligence", hash: "backup-radar", icon: "🔄", desc: "Unprotected PVC discovery, RPO tracking, and backup verification" },
  { title: "GitOps Drift Detector", category: "Intelligence", hash: "gitops-drift", icon: "🐙", desc: "Detect out-of-sync cluster resources and manual kubectl overrides" },
  { title: "Network Topology Visualizer", category: "Intelligence", hash: "topology", icon: "🕸️", desc: "Interactive cross-namespace service traffic and ingress graph" },
  { title: "Capsule Tenant Isolation Matrix", category: "Security", hash: "isolation-matrix", icon: "🛡️", desc: "Multi-tenant namespace boundaries and cross-tenant leak checks" },
  { title: "TLS Certificate Expiry Radar", category: "Security", hash: "certificates", icon: "📜", desc: "Expiring TLS certificates and cert-manager issuers" },
  { title: "Connect Kubernetes Cluster", category: "Cluster", hash: "cluster-connect", icon: "🔗", desc: "Upload kubeconfig to connect a new Kubernetes cluster" },
];

let selectedCommandPaletteIndex = 0;
let currentPaletteResults = [];

function openCommandPalette(initialQuery = "") {
  const modal = $("#admin-cmd-k-modal");
  if (!modal) return;
  modal.classList.add("open");
  const input = $("#cmd-k-input");
  input.value = initialQuery;
  input.focus();
  filterCommandPalette(initialQuery);
}

function closeCommandPalette() {
  const modal = $("#admin-cmd-k-modal");
  if (modal) modal.classList.remove("open");
}

function filterCommandPalette(q = "") {
  const query = q.toLowerCase().trim();
  if (!query) {
    currentPaletteResults = COMMAND_PALETTE_ITEMS.slice(0, 10);
  } else {
    currentPaletteResults = COMMAND_PALETTE_ITEMS.filter(item =>
      item.title.toLowerCase().includes(query) ||
      item.category.toLowerCase().includes(query) ||
      item.desc.toLowerCase().includes(query) ||
      item.hash.toLowerCase().includes(query)
    );
  }

  selectedCommandPaletteIndex = 0;
  renderCommandPaletteResults();
}

function renderCommandPaletteResults() {
  const container = $("#cmd-k-results");
  if (!container) return;

  if (currentPaletteResults.length === 0) {
    container.innerHTML = `
      <div style="padding: 24px; text-align: center; color: var(--admin-text-muted); font-size: 13px;">
        No actions or resources matched your query. Press Enter to search with AI Copilot.
      </div>
    `;
    return;
  }

  container.innerHTML = currentPaletteResults.map((item, idx) => `
    <div class="admin-cmd-k-item ${idx === selectedCommandPaletteIndex ? "selected" : ""}" data-index="${idx}" onclick="executePaletteItem(${idx})">
      <span class="admin-cmd-k-item-icon">${item.icon}</span>
      <div class="admin-cmd-k-item-details">
        <div class="admin-cmd-k-item-title">${escapeHTML(item.title)}</div>
        <div class="admin-cmd-k-item-desc">${escapeHTML(item.desc)}</div>
      </div>
      <span class="admin-cmd-k-item-category">${escapeHTML(item.category)}</span>
    </div>
  `).join("");
}

function executePaletteItem(idx) {
  const item = currentPaletteResults[idx];
  closeCommandPalette();
  if (!item) return;
  setRoute(item.hash);
}

function handleCommandPaletteKeydown(e) {
  if (e.key === "ArrowDown") {
    e.preventDefault();
    if (currentPaletteResults.length > 0) {
      selectedCommandPaletteIndex = (selectedCommandPaletteIndex + 1) % currentPaletteResults.length;
      renderCommandPaletteResults();
      scrollPaletteSelectionIntoView();
    }
  } else if (e.key === "ArrowUp") {
    e.preventDefault();
    if (currentPaletteResults.length > 0) {
      selectedCommandPaletteIndex = (selectedCommandPaletteIndex - 1 + currentPaletteResults.length) % currentPaletteResults.length;
      renderCommandPaletteResults();
      scrollPaletteSelectionIntoView();
    }
  } else if (e.key === "Enter") {
    e.preventDefault();
    if (currentPaletteResults.length > 0) {
      executePaletteItem(selectedCommandPaletteIndex);
    } else {
      // Fallback: send to AI Copilot
      const val = $("#cmd-k-input")?.value || "";
      closeCommandPalette();
      if (val) {
        setRoute("copilot");
      }
    }
  } else if (e.key === "Escape") {
    e.preventDefault();
    closeCommandPalette();
  }
}

function scrollPaletteSelectionIntoView() {
  const selectedEl = document.querySelector(".admin-cmd-k-item.selected");
  if (selectedEl) {
    selectedEl.scrollIntoView({ block: "nearest" });
  }
}

async function initializeAdmin() {
  try {
    const user = await request("/api/v1/me");
    if (user.platform_admin !== true) {
      $("#admin-content-area").innerHTML = `
        <div class="admin-empty-state">
          <h2>Access Denied</h2>
          <p>Your account does not have platform administrator access.</p>
          <a class="admin-btn admin-btn-primary" href="/dashboard">Return to Customer Workspace</a>
        </div>`;
      return;
    }
    const name = user.name || user.email || "Admin";
    $("#admin-avatar").textContent = name.charAt(0).toUpperCase();

    setupEventListeners();
    await Promise.all([loadClusterHeader(), loadNamespacesDropdown(), loadWorkspaceMappings()]);
    await renderCurrentView();

    // Start 10s auto-refresh by default (skips forms and active input)
    state.autoRefreshTimer = setInterval(() => {
      if (shouldAutoRefresh()) {
        loadClusterHeader();
        renderCurrentView();
      }
    }, 10000);
  } catch (err) {
    if (err.message !== "Sign in required") {
      $("#admin-content-area").innerHTML = `<div class="admin-empty-state">Failed to initialize: ${escapeHTML(err.message)}</div>`;
    }
  }
}

initializeAdmin();
