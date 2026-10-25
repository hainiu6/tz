const $ = (selector) => document.querySelector(selector);
const savedTheme = localStorage.getItem("tz-theme");
const initialTheme = savedTheme === "light" ? "light" : "dark";
document.documentElement.dataset.theme = initialTheme;
const defaultColumns = [
  { id: "sort", label: "排序", width: 40 }, { id: "server", label: "服务器", width: 152 },
  { id: "expiresAt", label: "到期时间", width: 190 },
  { id: "trafficQuota", label: "流量", width: 86 },
  { id: "note", label: "备注", width: 212 },
  { id: "uploadSpeed", label: "上传速度", width: 90 }, { id: "downloadSpeed", label: "下载速度", width: 90 },
  { id: "todayUpload", label: "今日上传", width: 74 }, { id: "todayDownload", label: "今日下载", width: 74 },
  { id: "yesterdayUpload", label: "昨日上传", width: 74 }, { id: "yesterdayDownload", label: "昨日下载", width: 74 },
  { id: "dayBeforeYesterdayUpload", label: "前天上传", width: 74 }, { id: "dayBeforeYesterdayDownload", label: "前天下载", width: 74 },
  { id: "totalUpload", label: "总上传", width: 74 }, { id: "totalDownload", label: "总下载", width: 74 },
  { id: "uptime", label: "运行时间", width: 136 }, { id: "cpu", label: "CPU", width: 64 },
  { id: "memory", label: "内存", width: 64 }, { id: "disk", label: "存储", width: 64 },
  { id: "actions", label: "操作", width: 82 },
];
const trafficUnits = ["B", "KB", "MB", "GB", "TB", "PB"];

function loadColumns() {
  let saved = [];
  try { saved = JSON.parse(localStorage.getItem("tz-table-columns") || "[]"); } catch (_) { saved = []; }
  const definitions = new Map(defaultColumns.map((column) => [column.id, column]));
  const seen = new Set(), columns = [];
  if (Array.isArray(saved)) saved.forEach((item) => {
    if (!item || !definitions.has(item.id) || seen.has(item.id)) return;
    const definition = definitions.get(item.id); seen.add(item.id);
    columns.push({ ...definition, visible: item.visible !== false });
  });
  defaultColumns.forEach((column) => {
    if (seen.has(column.id)) return;
    const value = { ...column, visible: true }; seen.add(column.id);
    const after = { expiresAt: "server", trafficQuota: "expiresAt", note: "trafficQuota", dayBeforeYesterdayUpload: "yesterdayDownload", dayBeforeYesterdayDownload: "dayBeforeYesterdayUpload" }[column.id];
    const anchorIndex = columns.findIndex((item) => item.id === after);
    if (anchorIndex >= 0) { columns.splice(anchorIndex + 1, 0, value); return; }
    columns.push(value);
  });
  if (!columns.some((column) => column.visible)) columns.find((column) => column.id === "server").visible = true;
  return columns;
}

const state = {
  groups: [], nodes: [], group: "", query: "", status: "", sortKey: "", sortDirection: "desc", theme: initialTheme,
  columns: loadColumns(),
  actionMenuNode: "",
  speedTestNode: "", speedTestStarting: false,
  token: localStorage.getItem("tz-admin-token") || "",
};

async function api(path, options = {}) {
  const response = await fetch(`/api/${path}`, {
    ...options,
    headers: { "Content-Type": "application/json", "X-TZ-Admin-Token": btoa(Array.from(new TextEncoder().encode(state.token), (byte) => String.fromCharCode(byte)).join("")), ...options.headers },
  });
  const body = await response.json().catch(() => ({}));
  if (response.status === 401) {
    state.token = "";
    localStorage.removeItem("tz-admin-token");
    $("#syncStatus").classList.add("error");
    $("#syncStatus").lastChild.textContent = " 需要管理令牌";
    if (!$("#formDialog").open) openToken("管理令牌错误，请重新填写");
    const error = new Error("管理令牌无效"); error.status = 401; throw error;
  }
  if (!response.ok) { const error = new Error(body.error || "操作失败"); error.status = response.status; throw error; }
  return body;
}

function bytes(value, speed = false) {
  if (value === undefined || value === null) return "—";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let unit = 0, size = value;
  while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit += 1; }
  const digits = size >= 100 || unit === 0 ? 0 : size >= 10 ? 1 : 2;
  return `${size.toFixed(digits)} ${units[unit]}${speed ? "/s" : ""}`;
}

function quantityHTML(formatted, unitClass) {
  const separator = formatted.lastIndexOf(" ");
  if (separator < 0) return escapeHTML(formatted);
  return `${escapeHTML(formatted.slice(0, separator))} <span class="quantity-unit ${unitClass}">${escapeHTML(formatted.slice(separator + 1))}</span>`;
}

const bytesHTML = (value) => quantityHTML(bytes(value), "traffic-unit");

function remainingHTML(number, unit, prefix = "剩") {
  return `<span class="remaining-label">${escapeHTML(prefix)}</span> <span class="remaining-number">${escapeHTML(number)}</span> <span class="remaining-unit">${escapeHTML(unit)}</span>`;
}

function trafficQuotaHTML(node) {
  const exhausted = node.trafficExhausted ? '<small class="quota-exhausted">流量已用完</small>' : "";
  if (node.trafficQuota === undefined || node.trafficQuota === null || String(node.trafficQuota).trim() === "") return exhausted;
  const amount = Number(node.trafficQuota), unit = node.trafficQuotaUnit || "TB";
  const unitIndex = trafficUnits.indexOf(unit), quota = amount * 1024 ** unitIndex;
  if (unitIndex < 0 || amount < 0 || !Number.isFinite(quota)) return exhausted;
  const remaining = quota - (Number(node.totalUpload) || 0);
  const [number, remainingUnit] = bytes(Math.abs(remaining)).split(" ");
  return `<span class="quota-value"><span class="quota-number">${escapeHTML(node.trafficQuota)}</span> <span class="quota-unit">${unit}</span></span>${exhausted || `<small class="quota-remaining${remaining < 0 ? " exceeded" : ""}">${remainingHTML(`${remaining < 0 ? "−" : ""}${number}`, remainingUnit)}</small>`}`;
}

function megabits(value) {
  const mbps = value * 8 / 1000000;
  return `${mbps.toFixed(mbps >= 100 ? 0 : mbps >= 10 ? 1 : 2)} M/s`;
}

function speedHTML(value) {
  const [number, unit] = megabits(value).split(" ");
  return `<strong class="speed-number">${number}</strong> <span class="speed-unit">${unit}</span>`;
}

const megabitsHTML = (value) => quantityHTML(megabits(value), "speed-unit");

function uptime(value) {
  if (value === undefined || value === null) return "—";
  const totalHours = Math.floor(Math.max(0, value) / 3600), days = Math.floor(totalHours / 24), hours = totalHours % 24;
  return days > 0 ? `${days}天 ${hours}小时` : `${hours}小时`;
}

function reportTime(lastSeen) {
  const timestamp = Date.parse(lastSeen);
  if (!Number.isFinite(timestamp) || timestamp <= 0) return "时间未知";
  const parts = Object.fromEntries(new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
  }).formatToParts(timestamp).map(({ type, value }) => [type, value]));
  return `${parts.year}-${parts.month}-${parts.day} ${parts.hour}:${parts.minute}:${parts.second}`;
}

function expiryInfo(value) {
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp) || timestamp <= 0) return null;
  const day = 86400000, hour = 3600000, remaining = timestamp - Date.now();
  const unit = remaining < 2 * day ? "小时" : "天";
  const count = Math.floor(remaining / (unit === "小时" ? hour : day));
  const prefix = remaining > 0 && remaining < hour ? "剩不足" : "剩";
  const number = remaining > 0 && remaining < hour ? 1 : count;
  const label = remaining <= 0 ? "已到期" : `${prefix}${number}${unit}`;
  const html = remaining <= 0 ? '<span class="remaining-label">已到期</span>' : remainingHTML(number, unit, prefix);
  return { date: reportTime(value).slice(0, 16), label, html, urgent: remaining < 2 * day };
}

function parseExpiryInput(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  const match = /^(\d{4}-\d{2}-\d{2})(?:[ T](\d{2}:\d{2})(?::(\d{2}))?)?$/.exec(text);
  const invalid = () => new Error("请输入有效日期，例如 2027-03-01 或 2027-03-01 12:30");
  if (!match) throw invalid();
  const normalized = `${match[1]} ${match[2] || "00:00"}:${match[3] || "00"}`;
  const date = new Date(`${normalized.replace(" ", "T")}+08:00`);
  if (!Number.isFinite(date.getTime()) || reportTime(date.toISOString()) !== normalized) throw invalid();
  return date.toISOString();
}

const percent = (used, total) => total ? Math.min(100, used / total * 100) : 0;
const online = (node) => Date.now() - new Date(node.lastSeen).getTime() < 15000;
const groupIDs = (node) => node.groupIds || (node.groupId ? [node.groupId] : []);
const escapeHTML = (value) => String(value ?? "").replace(/[&<>'"]/g, (char) => ({
  "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;",
})[char]);

function toast(message) {
  const element = $("#toast"); element.textContent = message; element.classList.add("show");
  setTimeout(() => element.classList.remove("show"), 2200);
}

function saveColumns() {
  localStorage.setItem("tz-table-columns", JSON.stringify(state.columns.map(({ id, visible }) => ({ id, visible }))));
}

function applyColumnPreferences() {
  const table = $(".table-wrap table"); if (!table) return;
  [$("#tableHeader"), ...document.querySelectorAll("#nodeRows tr")].forEach((row) => {
    if (!row) return;
    const cells = new Map([...row.querySelectorAll("[data-column]")].map((cell) => [cell.dataset.column, cell]));
    state.columns.forEach((column) => {
      const cell = cells.get(column.id); if (!cell) return;
      cell.hidden = !column.visible; row.appendChild(cell);
    });
  });
  const width = state.columns.filter((column) => column.visible).reduce((sum, column) => sum + column.width, 0);
  table.style.minWidth = `${Math.max(240, width)}px`;
}

function moveColumn(id, offset) {
  const index = state.columns.findIndex((column) => column.id === id), target = index + offset;
  if (index < 0 || target < 0 || target >= state.columns.length) return;
  const [column] = state.columns.splice(index, 1); state.columns.splice(target, 0, column);
  saveColumns(); applyColumnPreferences(); renderColumnSettings();
}

function renderColumnSettings() {
  $("#columnList").innerHTML = state.columns.map((column, index) => `<div class="column-item" draggable="true" data-column-item="${column.id}"><span class="drag-handle" title="拖动调整位置" aria-hidden="true">⠿</span><label><input type="checkbox" data-column-visible="${column.id}" ${column.visible ? "checked" : ""}><span>${column.label}</span></label><div class="column-move"><button type="button" data-column-up="${column.id}" aria-label="上移${column.label}" ${index === 0 ? "disabled" : ""}>↑</button><button type="button" data-column-down="${column.id}" aria-label="下移${column.label}" ${index === state.columns.length - 1 ? "disabled" : ""}>↓</button></div></div>`).join("");
  document.querySelectorAll("[data-column-visible]").forEach((checkbox) => { checkbox.onchange = () => {
    const column = state.columns.find((item) => item.id === checkbox.dataset.columnVisible);
    if (!checkbox.checked && state.columns.filter((item) => item.visible).length === 1) { checkbox.checked = true; toast("至少保留一列"); return; }
    column.visible = checkbox.checked; saveColumns(); applyColumnPreferences();
  }; });
  document.querySelectorAll("[data-column-up]").forEach((button) => { button.onclick = () => moveColumn(button.dataset.columnUp, -1); });
  document.querySelectorAll("[data-column-down]").forEach((button) => { button.onclick = () => moveColumn(button.dataset.columnDown, 1); });
  document.querySelectorAll("[data-column-item]").forEach((item) => {
    item.ondragstart = (event) => { event.dataTransfer.effectAllowed = "move"; event.dataTransfer.setData("text/plain", item.dataset.columnItem); item.classList.add("dragging"); };
    item.ondragend = () => { document.querySelectorAll(".column-item").forEach((element) => element.classList.remove("dragging", "drag-over")); };
    item.ondragover = (event) => { event.preventDefault(); event.dataTransfer.dropEffect = "move"; item.classList.add("drag-over"); };
    item.ondragleave = () => item.classList.remove("drag-over");
    item.ondrop = (event) => {
      event.preventDefault(); const sourceID = event.dataTransfer.getData("text/plain"), targetID = item.dataset.columnItem;
      if (!sourceID || sourceID === targetID) return;
      const sourceIndex = state.columns.findIndex((column) => column.id === sourceID), targetIndex = state.columns.findIndex((column) => column.id === targetID);
      if (sourceIndex < 0 || targetIndex < 0) return;
      const [column] = state.columns.splice(sourceIndex, 1); state.columns.splice(targetIndex, 0, column);
      saveColumns(); applyColumnPreferences(); renderColumnSettings();
    };
  });
}

async function load() {
	if (state.loading) return;
	state.loading = true;
  try {
    const dashboard = await api("dashboard");
    state.groups = dashboard.groups; state.nodes = dashboard.nodes;
    $("#syncStatus").classList.remove("error"); $("#syncStatus").lastChild.textContent = " 已同步";
    render();
  } catch (error) { if (error.status !== 401) toast(error.message); }
  finally { state.loading = false; }
}

function render() {
  const scoped = state.nodes.filter((node) => !state.group || groupIDs(node).includes(state.group));
  const active = scoped.filter(online);
  const uploadSpeed = active.reduce((sum, node) => sum + node.uploadSpeed, 0);
  const downloadSpeed = active.reduce((sum, node) => sum + node.downloadSpeed, 0);
  const totalUpload = scoped.reduce((sum, node) => sum + (node.totalUpload || 0), 0);
  const totalDownload = scoped.reduce((sum, node) => sum + (node.totalDownload || 0), 0);
  $("#sumUpSpeed").innerHTML = megabitsHTML(uploadSpeed); $("#sumDownSpeed").innerHTML = megabitsHTML(downloadSpeed);
  $("#sumTrafficSub").innerHTML = `上传总流量 ${bytesHTML(totalUpload)} · 下载总流量 ${bytesHTML(totalDownload)}`;
  $("#sumTodayUpload").innerHTML = bytesHTML(scoped.reduce((sum, node) => sum + (node.todayUpload || 0), 0));
  $("#sumTodayDownload").innerHTML = bytesHTML(scoped.reduce((sum, node) => sum + (node.todayDownload || 0), 0));
  $("#sumYesterdayUpload").innerHTML = bytesHTML(scoped.reduce((sum, node) => sum + (node.yesterdayUpload || 0), 0));
  $("#sumYesterdayDownload").innerHTML = bytesHTML(scoped.reduce((sum, node) => sum + (node.yesterdayDownload || 0), 0));
  $("#sumDayBeforeYesterdayUpload").innerHTML = bytesHTML(scoped.reduce((sum, node) => sum + (node.dayBeforeYesterdayUpload || 0), 0));
  $("#sumDayBeforeYesterdayDownload").innerHTML = bytesHTML(scoped.reduce((sum, node) => sum + (node.dayBeforeYesterdayDownload || 0), 0));
  $("#onlineCount").textContent = active.length;
  $("#offlineCount").textContent = scoped.length - active.length;
  document.querySelectorAll("[data-status]").forEach((button) => {
    const selected = button.dataset.status === state.status;
    button.classList.toggle("active", selected); button.setAttribute("aria-pressed", String(selected));
  });
  document.querySelectorAll(".daily-scope").forEach((element) => { element.textContent = state.group ? "北京时间 · 当前分组" : "北京时间 · 全部节点"; });
  renderGroups(); renderNodes(); renderSpeedTest();
}

function renderGroups() {
  const counts = {}; state.nodes.forEach((node) => { groupIDs(node).forEach((groupID) => { counts[groupID] = (counts[groupID] || 0) + 1; }); });
  $("#groupList").innerHTML = `<button class="group ${state.group === "" ? "active" : ""}" data-group=""><span class="group-label"><i class="dot all"></i><span class="group-name">全部服务器</span></span><b>${state.nodes.length}</b></button>` + state.groups.map((group) => `<div class="group-row"><button class="group ${state.group === group.id ? "active" : ""}" data-group="${group.id}" title="${escapeHTML(group.name)}"><span class="group-label"><i class="dot online"></i><span class="group-name">${escapeHTML(group.name)}</span></span><b>${counts[group.id] || 0}</b></button><button class="group-edit" data-group-edit="${group.id}" title="编辑分组">✎</button><button class="group-delete" data-group-delete="${group.id}" title="删除分组">×</button></div>`).join("");
  document.querySelectorAll(".group").forEach((button) => { button.onclick = () => { state.group = button.dataset.group; render(); }; });
  document.querySelectorAll("[data-group-edit]").forEach((button) => { button.onclick = () => openEntityForm("group", state.groups.find((group) => group.id === button.dataset.groupEdit)); });
  document.querySelectorAll("[data-group-delete]").forEach((button) => { button.onclick = () => deleteGroup(button.dataset.groupDelete); });
}

function renderNodes() {
  const tableScrollLeft = $(".table-wrap").scrollLeft;
  const filtered = state.nodes.filter((node) => (!state.group || groupIDs(node).includes(state.group)) && (!state.query || `${node.name} ${node.ip} ${node.note || ""}`.toLowerCase().includes(state.query)) && (!state.status || (state.status === "online") === online(node)));
  const nodes = state.sortKey ? filtered.map((node, index) => ({ node, index })).sort((left, right) => {
    const speedSort = state.sortKey === "uploadSpeed" || state.sortKey === "downloadSpeed";
    const leftValue = speedSort && !online(left.node) ? 0 : Number(left.node[state.sortKey]) || 0;
    const rightValue = speedSort && !online(right.node) ? 0 : Number(right.node[state.sortKey]) || 0;
    const result = leftValue - rightValue;
    return result === 0 ? left.index - right.index : (state.sortDirection === "asc" ? result : -result);
  }).map((item) => item.node) : filtered;
  const group = state.groups.find((item) => item.id === state.group);
  const statusLabel = state.status === "online" ? "在线" : state.status === "offline" ? "离线" : "";
  $("#listTitle").textContent = group ? group.name : "全部服务器"; $("#listMeta").textContent = `${nodes.length} 台${statusLabel}服务器`;
  $("#emptyState").hidden = nodes.length > 0;
  const noNodesAtAll = state.nodes.length === 0;
  $("#emptyTitle").textContent = noNodesAtAll ? "等待节点接入" : "没有匹配的服务器";
  $("#emptyText").textContent = noNodesAtAll ? "在任意 Linux 服务器运行同一条安装命令，节点会自动出现。" : "请切换状态、分组或修改搜索条件。";
  $("#emptyInstallBtn").hidden = !noNodesAtAll;
  document.querySelectorAll("[data-sort-heading]").forEach((heading) => {
    const selected = heading.dataset.sortHeading === state.sortKey;
    heading.setAttribute("aria-sort", selected ? (state.sortDirection === "asc" ? "ascending" : "descending") : "none");
    const indicator = heading.querySelector("span"); if (indicator) indicator.textContent = selected ? (state.sortDirection === "asc" ? "↑" : "↓") : "↕";
  });
  $("#nodeRows").innerHTML = nodes.map((node) => {
    const isOnline = online(node), memoryPercent = percent(node.memoryUsed, node.memoryTotal), diskPercent = percent(node.diskUsed, node.diskTotal);
    const lastSeen = reportTime(node.lastSeen);
    const expiry = expiryInfo(node.expiresAt);
    return `<tr class="${isOnline ? "" : "node-offline"}">
      <td class="sort-cell" data-column="sort" data-label="排序">${node.sort}</td>
      <td class="server-cell" data-column="server"><div class="server-name"><button class="server-copy" data-copy-ip="${escapeHTML(node.ip)}" title="点击复制 IP"><strong>${escapeHTML(node.name)}</strong><small class="server-meta"><span class="server-ip">${escapeHTML(node.ip || "等待首次上报")}</span>${isOnline ? "" : '<span class="server-status"> · 离线</span>'}</small></button></div></td>
      <td class="expiry-cell" data-column="expiresAt" data-label="到期时间"><div class="node-dates"><span class="expiry-time${expiry?.urgent ? " expiring" : ""}" title="到期时间（北京时间）">${expiry ? `<span class="expiry-date">${expiry.date}</span><small class="expiry-remaining">${expiry.html}</small>` : ""}</span></div></td>
      <td class="traffic-quota-cell" data-column="trafficQuota" data-label="流量">${trafficQuotaHTML(node)}</td>
      <td class="note-cell" data-column="note" data-label="备注"><span title="${escapeHTML(node.note || "")}">${escapeHTML(node.note || "—")}</span></td>
      <td class="speed up" data-column="uploadSpeed" data-label="上传速度">↑ ${isOnline ? speedHTML(node.uploadSpeed) : "—"}</td>
      <td class="speed down" data-column="downloadSpeed" data-label="下载速度">↓ ${isOnline ? speedHTML(node.downloadSpeed) : "—"}</td>
      <td class="traffic-cell" data-column="todayUpload" data-label="今日上传">${bytesHTML(node.todayUpload || 0)}</td>
      <td class="traffic-cell" data-column="todayDownload" data-label="今日下载">${bytesHTML(node.todayDownload || 0)}</td>
      <td class="traffic-cell" data-column="yesterdayUpload" data-label="昨日上传">${bytesHTML(node.yesterdayUpload || 0)}</td>
      <td class="traffic-cell" data-column="yesterdayDownload" data-label="昨日下载">${bytesHTML(node.yesterdayDownload || 0)}</td>
      <td class="traffic-cell" data-column="dayBeforeYesterdayUpload" data-label="前天上传">${bytesHTML(node.dayBeforeYesterdayUpload || 0)}</td>
      <td class="traffic-cell" data-column="dayBeforeYesterdayDownload" data-label="前天下载">${bytesHTML(node.dayBeforeYesterdayDownload || 0)}</td>
      <td class="traffic-cell" data-column="totalUpload" data-label="总上传">${bytesHTML(node.totalUpload)}</td>
      <td class="traffic-cell" data-column="totalDownload" data-label="总下载">${bytesHTML(node.totalDownload)}</td>
      <td class="uptime-cell" data-column="uptime" data-label="运行时间"><div class="node-uptime"><span>${uptime(node.uptime)}</span>${isOnline ? "" : `<small class="offline-time" aria-label="离线时间（北京时间，按最后上报记录）">${lastSeen}</small>`}</div></td>
      <td class="metric-cell" data-column="cpu" data-label="CPU"><div class="metric"><span class="value">${isOnline ? `${node.cpu.toFixed(1)}%` : "—"}</span><progress class="bar" max="100" value="${isOnline ? Math.min(100, node.cpu) : 0}" aria-label="CPU 使用率"></progress></div></td>
      <td class="metric-cell" data-column="memory" data-label="内存"><div class="metric"><span class="value">${isOnline ? `${memoryPercent.toFixed(1)}%` : "—"}</span><progress class="bar" max="100" value="${isOnline ? memoryPercent : 0}" aria-label="内存使用率"></progress></div></td>
      <td class="metric-cell" data-column="disk" data-label="存储"><div class="metric"><span class="value">${isOnline ? `${diskPercent.toFixed(1)}%` : "—"}</span><progress class="bar" max="100" value="${isOnline ? diskPercent : 0}" aria-label="存储使用率"></progress></div></td>
      <td class="actions-cell" data-column="actions"><div class="row-actions"><button type="button" class="action edit-action" title="编辑" aria-label="编辑服务器" data-edit="${node.id}"><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m16 3 5 5-12 12-6 1 1-6Z"/><path d="m14 5 5 5"/></svg></button><button type="button" class="action more-action" title="更多操作" aria-label="更多操作" aria-haspopup="menu" aria-expanded="false" aria-controls="nodeActionMenu" data-node-menu="${node.id}">⋯</button></div></td>
    </tr>`;
  }).join("");
  applyColumnPreferences();
  $(".table-wrap").scrollLeft = tableScrollLeft;
  document.querySelectorAll("[data-edit]").forEach((button) => { button.onclick = () => editNode(button.dataset.edit); });
  document.querySelectorAll("[data-node-menu]").forEach((button) => { button.onclick = () => {
    if (state.actionMenuNode === button.dataset.nodeMenu) { closeNodeActionMenu(); return; }
    closeNodeActionMenu(); state.actionMenuNode = button.dataset.nodeMenu; syncNodeActionMenu();
    $("#nodeActionMenu button:not(:disabled)")?.focus();
  }; });
  syncNodeActionMenu();
  document.querySelectorAll("[data-copy-ip]").forEach((button) => { button.onclick = () => copyText(button.dataset.copyIp).then(() => toast(`已复制 ${button.dataset.copyIp}`)).catch((error) => toast(error.message)); });
}

function closeNodeActionMenu() {
  state.actionMenuNode = "";
  $("#nodeActionMenu").hidden = true;
  document.querySelectorAll("[data-node-menu]").forEach((button) => button.setAttribute("aria-expanded", "false"));
}

function syncNodeActionMenu() {
  if (!state.actionMenuNode) return;
  const node = state.nodes.find((item) => item.id === state.actionMenuNode);
  const anchor = [...document.querySelectorAll("[data-node-menu]")].find((button) => button.dataset.nodeMenu === state.actionMenuNode);
  if (!node || !anchor || anchor.closest("td").hidden) { closeNodeActionMenu(); return; }
  const menu = $("#nodeActionMenu"), rect = anchor.getBoundingClientRect();
  const tableRect = anchor.closest(".table-wrap").getBoundingClientRect();
  if (rect.right <= Math.max(0, tableRect.left) || rect.left >= Math.min(window.innerWidth, tableRect.right) || rect.bottom <= 0 || rect.top >= window.innerHeight) { closeNodeActionMenu(); return; }
  menu.querySelectorAll("[data-menu-action]").forEach((button) => {
    const action = button.dataset.menuAction;
    button.disabled = action === "upgrade" ? !online(node) : (action === "reboot" || action === "shutdown") && (!online(node) || !!node.powerRequested);
  });
  menu.hidden = false; anchor.setAttribute("aria-expanded", "true");
  const gap = 6;
  const left = Math.max(8, Math.min(rect.right - menu.offsetWidth, window.innerWidth - menu.offsetWidth - 8));
  const top = rect.bottom + gap + menu.offsetHeight <= window.innerHeight - 8 ? rect.bottom + gap : rect.top - gap - menu.offsetHeight;
  menu.style.left = `${left}px`; menu.style.top = `${Math.max(8, top)}px`;
}

async function resetNodeTraffic(id) {
  const node = state.nodes.find((item) => item.id === id); if (!node) return;
  if (!confirm(`确定清空“${node.name}”的流量记录？\n今日、昨日、前天和累计上传/下载都将归零，无法撤销。下一次上报建立新基线，之后开始统计新增流量。`)) return;
  try { await api(`nodes/${id}/reset-traffic`, { method: "POST" }); await load(); toast("流量记录已清空"); }
  catch (error) { if (error.status !== 401) toast(error.message); }
}

function renderSpeedTest() {
  if (!state.speedTestNode) return;
  const node = state.nodes.find((item) => item.id === state.speedTestNode);
  const job = node?.speedTest, pending = job && (job.status === "pending" || job.status === "running");
  const supported = !!node?.speedTestSupported, isOnline = node && online(node);
  $("#speedTestCurrentUpload").innerHTML = isOnline ? megabitsHTML(node.uploadSpeed || 0) : "—";
  $("#speedTestCurrentDownload").innerHTML = isOnline ? megabitsHTML(node.downloadSpeed || 0) : "—";
  $("#speedTestLiveHint").textContent = isOnline ? "随节点上报自动刷新" : "节点离线，实时速度不可用";
  $("#speedTestTitle").textContent = node ? `${node.name} · 测速` : "服务器已删除";
  $("#speedTestStatus").textContent = !node ? "服务器已不存在" : pending ? (job.status === "pending" ? "等待节点接收测速指令…" : "节点测速中，完成后自动显示结果…") : !supported ? "请先通过操作菜单升级探针，或点击顶部“全部升级”" : !isOnline ? "节点离线，可查看上次结果，暂时无法发起测速" : job ? (job.status === "completed" ? "测速完成" : "测速失败或部分方向未完成，可稍后重试") : "尚未测速";
  const finished = job && !pending;
  $("#speedTestUpload").innerHTML = finished && !job.uploadError && !job.error ? megabitsHTML(job.uploadBps) : "—";
  $("#speedTestDownload").innerHTML = finished && !job.downloadError && !job.error ? megabitsHTML(job.downloadBps) : "—";
  $("#speedTestTime").textContent = job ? `${job.completedAt && !pending ? "完成" : "发起"}时间：${reportTime(job.completedAt && !pending ? job.completedAt : job.requestedAt)}（北京时间） · Cloudflare` : "";
  $("#speedTestError").textContent = !pending && job ? [job.error, job.uploadError ? `上传：${job.uploadError}` : "", job.downloadError ? `下载：${job.downloadError}` : ""].filter(Boolean).join("；") : "";
  const cooldown = job && Date.now() - Date.parse(job.requestedAt) < 60000;
  $("#speedTestStart").disabled = !isOnline || !supported || !!pending || !!cooldown || state.speedTestStarting;
  $("#speedTestStart").textContent = pending || state.speedTestStarting ? "测速中…" : cooldown ? "请间隔 1 分钟再测" : job ? "重新测速" : "开始测速";
}

function openSpeedTest(id) {
  state.speedTestNode = id;
  renderSpeedTest();
  $("#speedTestDialog").showModal();
}

async function startSpeedTest() {
  const node = state.nodes.find((item) => item.id === state.speedTestNode);
  if (!node || state.speedTestStarting || $("#speedTestStart").disabled) return;
  state.speedTestStarting = true; renderSpeedTest();
  try {
    const result = await api(`nodes/${node.id}/speedtest`, { method: "POST" });
    node.speedTest = result.speedTest;
    await load();
  } catch (error) { if (error.status !== 401) toast(error.message); }
  finally { state.speedTestStarting = false; renderSpeedTest(); }
}

function openEntityForm(kind, item = null) {
  const isNode = kind === "node";
  $("#formDialog").classList.toggle("node-editor", isNode);
  const expiryTime = isNode && item.expiresAt ? reportTime(item.expiresAt) : "时间未知";
  const expiryInput = expiryTime === "时间未知" ? "" : expiryTime.slice(0, 16);
  const trafficField = isNode ? `<div class="field"><label for="nodeTrafficQuota">流量</label><div class="quota-inputs"><input id="nodeTrafficQuota" name="trafficQuota" type="number" min="0" step="any" placeholder="例如 1" value="${escapeHTML(item.trafficQuota || "")}"><select name="trafficQuotaUnit" aria-label="流量单位">${trafficUnits.map((unit) => `<option value="${unit}" ${(item.trafficQuotaUnit || "TB") === unit ? "selected" : ""}>${unit}</option>`).join("")}</select></div><small class="field-help">默认 TB；剩余流量 = 填写流量 − 总上传，可留空</small><label class="traffic-exhausted-toggle"><input name="trafficExhausted" type="checkbox" ${item.trafficExhausted ? "checked" : ""}>流量已用完</label><small class="field-help">仅手动标记，不修改统计；取消勾选可恢复剩余流量显示</small></div>` : "";
  const nodeGroups = isNode ? groupIDs(item) : [];
  const groupPicker = isNode ? `<div class="field"><label>所属分组</label><details class="server-picker"><summary><span id="groupSummary">已选择 ${nodeGroups.length} 个分组</span><i>⌄</i></summary><div class="server-options">${state.groups.map((group) => `<label class="server-option"><input class="group-choice" type="checkbox" value="${group.id}" ${nodeGroups.includes(group.id) ? "checked" : ""}><span>${escapeHTML(group.name)}</span></label>`).join("") || '<div class="no-options">暂无分组</div>'}</div></details></div>` : "";
  const selectedCount = !isNode && item ? state.nodes.filter((node) => groupIDs(node).includes(item.id)).length : 0;
  const serverPicker = !isNode && item ? `<div class="field"><label>分组服务器</label><details class="server-picker"><summary><span id="memberSummary">已选择 ${selectedCount} 台</span><i>⌄</i></summary><div class="server-options"><div class="picker-search"><span>⌕</span><input id="serverPickerSearch" type="search" placeholder="搜索名称或 IP" autocomplete="off"></div><label class="server-option select-all"><input type="checkbox" id="selectAllNodes" ${state.nodes.length > 0 && selectedCount === state.nodes.length ? "checked" : ""}><span>全选筛选结果</span><b id="visibleNodeCount">${state.nodes.length}</b></label>${state.nodes.map((node) => `<label class="server-option node-option"><input class="node-choice" type="checkbox" value="${node.id}" ${groupIDs(node).includes(item.id) ? "checked" : ""}><span>${escapeHTML(node.name)}<small>${escapeHTML(node.ip)}</small></span></label>`).join("") || '<div class="no-options">暂无服务器</div>'}</div></details><small class="field-help">这里只修改当前分组，不影响服务器所在的其他分组</small></div>` : "";
  $("#modalKicker").textContent = isNode ? "服务器" : "分组";
  $("#modalTitle").textContent = item ? `编辑${isNode ? "服务器" : "分组"}` : "添加分组";
  $("#formFields").innerHTML = isNode
    ? `<div class="editor-column"><div class="field"><label>服务器名称</label><input name="name" required maxlength="60" value="${escapeHTML(item.name)}"></div><div class="field"><label for="nodeExpiresAt">到期时间（北京时间）</label><input id="nodeExpiresAt" name="expiresAt" type="text" autocomplete="off" maxlength="16" placeholder="2027-03-01 12:30" value="${escapeHTML(expiryInput)}"><small class="field-help">手动填写日期或日期时间，无需秒数，可留空</small></div>${trafficField}<div class="field"><label>排序</label><input name="sort" type="number" value="${item.sort}"></div></div><div class="editor-column"><div class="field"><label>备注</label><textarea name="note" maxlength="500" placeholder="填写机房、线路或用途等信息">${escapeHTML(item.note || "")}</textarea></div>${groupPicker}</div>`
    : `<div class="field"><label>分组名称</label><input name="name" required maxlength="40" value="${escapeHTML(item?.name || "")}"></div><div class="field"><label>排序</label><input name="sort" type="number" value="${item?.sort ?? state.groups.length}"></div>${serverPicker}`;
  if (isNode) {
    setupGroupPicker();
    $("#nodeExpiresAt").oninput = (event) => event.target.setCustomValidity("");
  }
  if (!isNode && item) setupServerPicker();
  $("#entityForm").onsubmit = async (event) => {
    event.preventDefault(); const body = Object.fromEntries(new FormData(event.target)); body.sort = Number(body.sort) || 0;
    if (isNode) {
      body.trafficExhausted = event.target.elements.trafficExhausted.checked;
      body.groupIds = [...document.querySelectorAll(".group-choice:checked")].map((checkbox) => checkbox.value);
      if (!body.trafficQuota) body.trafficQuotaUnit = "";
      try { body.expiresAt = expiryInput && body.expiresAt.trim() === expiryInput ? item.expiresAt : parseExpiryInput(body.expiresAt); }
      catch (error) { $("#nodeExpiresAt").setCustomValidity(error.message); $("#nodeExpiresAt").reportValidity(); return; }
    }
    if (!isNode && item) body.nodeIds = [...document.querySelectorAll(".node-choice:checked")].map((checkbox) => checkbox.value);
    const path = item ? `${isNode ? "nodes" : "groups"}/${item.id}` : "groups";
    try { await api(path, { method: item ? "PUT" : "POST", body: JSON.stringify(body) }); $("#formDialog").close(); await load(); toast("已保存"); }
    catch (error) { if (error.status !== 401) toast(error.message); }
  };
  $("#formDialog").showModal(); setTimeout(() => $("#formFields input")?.focus(), 50);
}

function setupGroupPicker() {
  const choices = [...document.querySelectorAll(".group-choice")], summary = $("#groupSummary");
  const update = () => { summary.textContent = `已选择 ${choices.filter((checkbox) => checkbox.checked).length} 个分组`; };
  choices.forEach((checkbox) => { checkbox.onchange = update; });
  update();
}

function setupServerPicker() {
  const all = $("#selectAllNodes"), choices = [...document.querySelectorAll(".node-choice")], summary = $("#memberSummary"), search = $("#serverPickerSearch"), visibleCount = $("#visibleNodeCount");
  const visibleChoices = () => choices.filter((checkbox) => !checkbox.closest(".node-option").hidden);
  const update = () => {
    const count = choices.filter((checkbox) => checkbox.checked).length;
    const visible = visibleChoices(), visibleSelected = visible.filter((checkbox) => checkbox.checked).length;
    summary.textContent = `已选择 ${count} 台`;
    visibleCount.textContent = visible.length;
    all.checked = visible.length > 0 && visibleSelected === visible.length;
    all.indeterminate = visibleSelected > 0 && visibleSelected < visible.length;
  };
  search.oninput = () => {
    const query = search.value.trim().toLowerCase();
    choices.forEach((checkbox) => { checkbox.closest(".node-option").hidden = query !== "" && !checkbox.closest(".node-option").textContent.toLowerCase().includes(query); });
    update();
  };
  all.onchange = () => { visibleChoices().forEach((checkbox) => { checkbox.checked = all.checked; }); update(); };
  choices.forEach((checkbox) => { checkbox.onchange = update; });
  update();
}

const editNode = (id) => openEntityForm("node", state.nodes.find((node) => node.id === id));
async function deleteNode(id) {
  const node = state.nodes.find((item) => item.id === id);
  if (!confirm(`确定删除“${node.name}”并卸载这台服务器上的探针？离线节点将在重新上线后执行；旧版探针会先升级再卸载。`)) return;
  try { await api(`nodes/${id}`, { method: "DELETE" }); await load(); toast("服务器已移除，卸载指令已排队，等待节点执行"); }
  catch (error) { if (error.status !== 401) toast(error.message); }
}
async function upgradeNode(id) {
  const node = state.nodes.find((item) => item.id === id);
  if (!online(node)) { toast("服务器离线，暂时无法升级"); return; }
  if (!confirm(`向“${node.name}”发送探针升级指令？`)) return;
  try { await api(`nodes/${id}/upgrade`, { method: "POST" }); toast("升级指令已发送"); }
  catch (error) { if (error.status !== 401) toast(error.message); }
}
async function upgradeAllNodes() {
  if (!state.nodes.length) { toast("暂无节点可以升级"); return; }
  if (!confirm(`向全部 ${state.nodes.length} 台节点发送探针升级指令？离线节点会在重新上线后执行。`)) return;
  try {
    const result = await api("nodes/upgrade-all", { method: "POST" });
    toast(`已为 ${result.count} 台节点排队升级`);
  } catch (error) { if (error.status !== 401) toast(error.message); }
}
async function powerNode(id, action) {
  const node = state.nodes.find((item) => item.id === id);
  if (!node || !online(node)) { toast("节点离线，不能发送电源指令"); return; }
  const shutdown = action === "shutdown";
  const warning = shutdown ? "关机后无法通过面板远程开机，请确认有其他开机方式。" : "重启会暂时断开节点连接，请先保存节点上的工作。";
  if (!confirm(`确定要${shutdown ? "关机" : "重启"}“${node.name}”（${node.ip}）吗？\n${warning}`)) return;
  try {
    await api(`nodes/${id}/power`, { method: "POST", body: JSON.stringify({ action }) });
    await load();
    toast(`${shutdown ? "关机" : "重启"}指令已排队，在线探针将很快执行`);
  } catch (error) { if (error.status !== 401) toast(error.message); }
}
async function deleteGroup(id) {
  const group = state.groups.find((item) => item.id === id);
  if (!confirm(`确定删除分组“${group.name}”？组内服务器将变为未分组。`)) return;
  try { await api(`groups/${id}`, { method: "DELETE" }); if (state.group === id) state.group = ""; await load(); toast("分组已删除"); }
  catch (error) { if (error.status !== 401) toast(error.message); }
}

function openToken(message = "") {
  if ($("#formDialog").open) return;
  $("#formDialog").classList.remove("node-editor");
  $("#modalKicker").textContent = "安全验证"; $("#modalTitle").textContent = "管理令牌";
  $("#formFields").innerHTML = `<div class="field"><label>管理令牌</label><input name="token" type="password" required autocomplete="current-password"><small class="field-help">${escapeHTML(message || "填写当前管理令牌")}</small></div>`;
  $("#entityForm").onsubmit = (event) => { event.preventDefault(); state.token = new FormData(event.target).get("token"); localStorage.setItem("tz-admin-token", state.token); $("#formDialog").close(); load(); };
  $("#formDialog").showModal(); setTimeout(() => $("#formFields input")?.focus(), 50);
}

function openChangeToken() {
  if ($("#formDialog").open) return;
  $("#formDialog").classList.remove("node-editor");
  $("#modalKicker").textContent = "安全设置"; $("#modalTitle").textContent = "修改管理令牌";
  $("#formFields").innerHTML = `<div class="field"><label>新管理令牌</label><input name="token" type="password" required autocomplete="new-password"></div><div class="field"><label>确认新令牌</label><input name="confirmToken" type="password" required autocomplete="new-password"></div><small class="field-help">令牌不设长度和复杂度要求，填写非空内容即可。修改后其他浏览器需重新登录。</small>`;
  $("#entityForm").onsubmit = async (event) => {
    event.preventDefault(); const form = new FormData(event.target), token = form.get("token");
    if (token !== form.get("confirmToken")) { toast("两次输入的令牌不一致"); return; }
    try {
      await api("admin-token", { method: "PUT", body: JSON.stringify({ token }) });
      state.token = token; localStorage.setItem("tz-admin-token", token); $("#formDialog").close(); toast("管理令牌已修改");
    } catch (error) { if (error.status !== 401) toast(error.message); }
  };
  $("#formDialog").showModal(); setTimeout(() => $("#formFields input")?.focus(), 50);
}

async function copyText(text) {
  if (!text) throw new Error("没有可复制的内容");
  if (navigator.clipboard && window.isSecureContext) {
    try { await navigator.clipboard.writeText(text); return; } catch (_) { /* use the HTTP-compatible fallback */ }
  }
  const textarea = document.createElement("textarea"); textarea.value = text; textarea.style.position = "fixed"; textarea.style.opacity = "0";
  textarea.setAttribute("readonly", "");
  const container = document.querySelector("dialog[open]") || document.body;
  container.appendChild(textarea); textarea.focus(); textarea.select();
  const copied = document.execCommand("copy"); textarea.remove();
  if (!copied) throw new Error("复制失败，请长按或手动选择命令");
}

async function openInstallCommand() {
  try {
    const config = await api("install");
    $("#installCommand").textContent = `curl -fsSL ${location.origin}/install.sh | bash -s -- --url ${location.origin} --token ${config.agentToken}`;
    $("#uninstallCommand").textContent = `curl -fsSL ${location.origin}/uninstall.sh | bash`;
    $("#commandDialog").showModal();
  } catch (error) { if (error.status !== 401) toast(error.message); }
}

function registerWebMCP() {
  const context = document.modelContext; if (!context?.registerTool) return;
  const register = (tool) => Promise.resolve(context.registerTool(tool)).catch(() => {});
  register({ name: "get_server_overview", title: "获取服务器概览", description: "读取当前面板中的服务器、分组和在线状态。", inputSchema: { type: "object", properties: {}, additionalProperties: false }, annotations: { readOnlyHint: true, untrustedContentHint: false }, execute: async () => { const dashboard = await api("dashboard"); return { groups: dashboard.groups.map((group) => ({ id: group.id, name: group.name })), servers: dashboard.nodes.map((node) => ({ id: node.id, name: node.name, ip: node.ip, online: online(node), cpu: node.cpu })) }; } });
  register({ name: "get_agent_install_command", title: "获取探针命令", description: "获取可在所有 Linux 节点重复使用的探针安装和卸载命令。", inputSchema: { type: "object", properties: {}, additionalProperties: false }, annotations: { readOnlyHint: true, untrustedContentHint: false }, execute: async () => { const config = await api("install"); return { installCommand: `curl -fsSL ${location.origin}/install.sh | bash -s -- --url ${location.origin} --token ${config.agentToken}`, uninstallCommand: `curl -fsSL ${location.origin}/uninstall.sh | bash` }; } });
}

document.querySelectorAll("[data-close-form]").forEach((button) => { button.onclick = () => $("#formDialog").close(); });
$("#nodeActionMenu").onclick = (event) => {
  const button = event.target.closest("[data-menu-action]"); if (!button || button.disabled) return;
  const id = state.actionMenuNode, action = button.dataset.menuAction; closeNodeActionMenu();
  if (action === "upgrade") upgradeNode(id);
  else if (action === "speedtest") openSpeedTest(id);
  else if (action === "reboot" || action === "shutdown") powerNode(id, action);
  else if (action === "reset-traffic") resetNodeTraffic(id);
  else if (action === "delete") deleteNode(id);
};
document.addEventListener("click", (event) => {
  if (!event.target.closest("#nodeActionMenu, [data-node-menu]")) closeNodeActionMenu();
});
document.addEventListener("keydown", (event) => {
  if (!state.actionMenuNode) return;
  if (event.key === "Escape") {
    const anchor = [...document.querySelectorAll("[data-node-menu]")].find((button) => button.dataset.nodeMenu === state.actionMenuNode);
    closeNodeActionMenu(); anchor?.focus(); event.preventDefault(); return;
  }
  if (!event.target.closest("#nodeActionMenu")) return;
  if (event.key === "Tab") {
    const anchor = [...document.querySelectorAll("[data-node-menu]")].find((button) => button.dataset.nodeMenu === state.actionMenuNode);
    closeNodeActionMenu(); anchor?.focus(); return;
  }
  if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
  const buttons = [...document.querySelectorAll("#nodeActionMenu button:not(:disabled)")];
  const index = buttons.indexOf(document.activeElement);
  const next = event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 : (index + (event.key === "ArrowDown" ? 1 : -1) + buttons.length) % buttons.length;
  buttons[next]?.focus(); event.preventDefault();
});
document.addEventListener("scroll", syncNodeActionMenu, true);
window.addEventListener("resize", syncNodeActionMenu);
$("#themeSelect").value = state.theme;
$("#themeSelect").onchange = (event) => { state.theme = event.target.value === "light" ? "light" : "dark"; document.documentElement.dataset.theme = state.theme; localStorage.setItem("tz-theme", state.theme); };
document.querySelectorAll("[data-status]").forEach((button) => { button.onclick = () => { state.status = state.status === button.dataset.status ? "" : button.dataset.status; renderNodes(); document.querySelectorAll("[data-status]").forEach((item) => { const selected = item.dataset.status === state.status; item.classList.toggle("active", selected); item.setAttribute("aria-pressed", String(selected)); }); }; });
document.querySelectorAll("[data-sort]").forEach((button) => { button.onclick = () => { const key = button.dataset.sort; if (state.sortKey === key) state.sortDirection = state.sortDirection === "desc" ? "asc" : "desc"; else { state.sortKey = key; state.sortDirection = "desc"; } renderNodes(); }; });
$("#installAgentBtn").onclick = openInstallCommand; $("#emptyInstallBtn").onclick = openInstallCommand;
$("#addGroupBtn").onclick = () => openEntityForm("group"); $("#tokenBtn").onclick = () => state.token ? openChangeToken() : openToken();
$("#search").oninput = (event) => { state.query = event.target.value.trim().toLowerCase(); renderNodes(); };
$("#upgradeAllBtn").onclick = upgradeAllNodes;
$("#speedTestStart").onclick = startSpeedTest;
$("#speedTestClose").onclick = $("#speedTestDone").onclick = () => $("#speedTestDialog").close();
$("#speedTestDialog").addEventListener("close", () => { state.speedTestNode = ""; });
$("#copyInstallCommand").onclick = () => copyText($("#installCommand").textContent).then(() => toast("安装命令已复制")).catch((error) => toast(error.message));
$("#copyUninstallCommand").onclick = () => copyText($("#uninstallCommand").textContent).then(() => toast("卸载命令已复制")).catch((error) => toast(error.message));
$("#commandDone").onclick = () => $("#commandDialog").close();
$("#columnsBtn").onclick = () => { renderColumnSettings(); $("#columnsDialog").showModal(); };
$("#columnsClose").onclick = $("#columnsDone").onclick = () => $("#columnsDialog").close();
$("#columnsReset").onclick = () => { state.columns = defaultColumns.map((column) => ({ ...column, visible: true })); saveColumns(); applyColumnPreferences(); renderColumnSettings(); toast("已恢复默认列设置"); };
applyColumnPreferences();
if (state.token) load(); else openToken();
setInterval(() => { if (state.token) load(); }, 3000);
registerWebMCP();
