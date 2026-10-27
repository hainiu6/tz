const ddnsState = { tasks: [], configured: false, editing: "" };
const ddnsRepeatLabels = { once: "仅一次", daily: "每天", weekly: "每周", monthly: "每月" };
const ddnsStatusLabels = { created: "已创建", updated: "已更新", unchanged: "无需修改", failed: "失败" };
$("#ddnsSecurityWarning").hidden = location.protocol === "https:" || ["localhost", "127.0.0.1", "[::1]"].includes(location.hostname);

function ddnsBeijingInput(date) {
  return new Date(new Date(date).getTime() + 8 * 3600000).toISOString().slice(0, 16);
}
function ddnsDisplayTime(date) {
  if (!date || date.startsWith("0001-")) return "—";
  return new Intl.DateTimeFormat("zh-CN", { timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(date));
}
function ddnsNavigate(open) {
  $("#dashboardPage").hidden = open;
  $("#ddnsPage").hidden = !open;
  $("#ddnsNavBtn").classList.toggle("ddns-active", open);
  if (open) ddnsRefresh();
}
async function ddnsRefresh() {
  if (!state.token) return;
  try {
    const data = await api("ddns");
    ddnsState.tasks = data.tasks || [];
    ddnsState.configured = !!data.configured;
    $("#ddnsCredentialStatus").textContent = data.configured ? "已配置 · 可重新填写以更换密钥" : "尚未配置，请先填写密钥";
    ddnsRender();
  } catch (error) { if (error.status !== 401) toast(error.message); }
}
function ddnsRender() {
  $("#ddnsEmpty").hidden = ddnsState.tasks.length > 0;
  $("#ddnsRows").innerHTML = ddnsState.tasks.map((task) => {
    const completed = task.repeat === "once" && task.completed;
    const status = completed ? "已执行" : task.enabled ? "已开启" : "已暂停";
    const nextRun = completed ? "—" : ddnsDisplayTime(task.nextRun);
    return `<tr><td class="ddns-domain">${escapeHTML(task.domain)}</td><td>${escapeHTML(task.type)}</td><td class="ddns-target" title="${escapeHTML(task.target)}">${escapeHTML(task.target)}</td><td>${escapeHTML(nextRun)}</td><td>${escapeHTML(ddnsRepeatLabels[task.repeat] || task.repeat)}</td><td><span class="ddns-state ${task.enabled ? "on" : "off"}">${status}</span>${task.lastStatus ? `<small class="ddns-last ${task.lastStatus === "failed" ? "error" : ""}">${escapeHTML(ddnsStatusLabels[task.lastStatus] || task.lastStatus)}</small>` : ""}</td><td><div class="ddns-actions"><button type="button" data-ddns-action="run" data-id="${task.id}">立即执行</button><button type="button" data-ddns-action="edit" data-id="${task.id}">编辑</button><button type="button" data-ddns-action="logs" data-id="${task.id}">日志</button><button type="button" data-ddns-action="toggle" data-id="${task.id}">${task.enabled ? "暂停" : "开启"}</button><button type="button" data-ddns-action="delete" data-id="${task.id}" class="danger">删除</button></div></td></tr>`;
  }).join("");
}
function ddnsOpenTask(task) {
  ddnsState.editing = task?.id || "";
  $("#ddnsTaskTitle").textContent = task ? "编辑解析任务" : "新建解析任务";
  $("#ddnsDomain").value = task?.domain || "";
  $("#ddnsType").value = task?.type || "A";
  $("#ddnsTarget").value = task?.target || "";
  $("#ddnsNextRun").value = task ? ddnsBeijingInput(task.nextRun) : ddnsBeijingInput(new Date(Date.now() + 5 * 60000));
  $("#ddnsRepeat").value = task?.repeat || "once";
  ddnsUpdateTargetHint();
  $("#ddnsTaskDialog").showModal();
}
function ddnsUpdateTargetHint() {
  $("#ddnsTarget").placeholder = $("#ddnsType").value === "CNAME" ? "例如 target.example.com" : "例如 1.2.3.4";
}
async function ddnsAction(button) {
  const task = ddnsState.tasks.find((item) => item.id === button.dataset.id);
  if (!task) return;
  const action = button.dataset.ddnsAction;
  if (action === "edit") { ddnsOpenTask(task); return; }
  if (action === "logs") {
    try {
      const result = await api(`ddns/tasks/${task.id}/logs`);
      $("#ddnsLogTitle").textContent = task.domain;
      $("#ddnsLogList").innerHTML = (result.logs || []).map((entry) => `<div class="ddns-log ${entry.status === "failed" ? "error" : ""}"><div><strong>${escapeHTML(ddnsStatusLabels[entry.status] || entry.status)}</strong><span>${escapeHTML(ddnsDisplayTime(entry.at))} · ${entry.trigger === "manual" ? "手动" : "定时"}</span></div><p>${escapeHTML(entry.from || "无原记录")} → ${escapeHTML(entry.to)}</p>${entry.message ? `<small>${escapeHTML(entry.message)}</small>` : ""}</div>`).join("") || '<p class="ddns-empty">暂无执行记录</p>';
      $("#ddnsLogDialog").showModal();
    } catch (error) { if (error.status !== 401) toast(error.message); }
    return;
  }
  if (action === "delete" && !confirm(`删除 ${task.domain} 的解析任务及日志？此操作不会删除华为云上的 DNS 记录。`)) return;
  button.disabled = true;
  try {
    if (action === "run") {
      toast("正在连接华为云，请稍候…");
      const result = await api(`ddns/tasks/${task.id}/run`, { method: "POST" });
      toast(result.log.status === "failed" ? `执行失败：${result.log.message}` : `执行完成：${ddnsStatusLabels[result.log.status]}`);
    } else if (action === "toggle") {
      await api(`ddns/tasks/${task.id}/enabled`, { method: "PUT", body: JSON.stringify({ enabled: !task.enabled }) });
      toast(task.enabled ? "任务已暂停" : "任务已开启");
    } else if (action === "delete") {
      await api(`ddns/tasks/${task.id}`, { method: "DELETE" });
      toast("任务已删除");
    }
    await ddnsRefresh();
  } catch (error) { if (error.status !== 401) toast(error.message); }
  finally { button.disabled = false; }
}

$("#ddnsNavBtn").onclick = () => ddnsNavigate($("#ddnsPage").hidden);
$("#homeLink").onclick = (event) => { event.preventDefault(); ddnsNavigate(false); window.scrollTo({ top: 0, behavior: "smooth" }); };
$("#ddnsBackBtn").onclick = () => ddnsNavigate(false);
$("#ddnsAddBtn").onclick = () => ddnsOpenTask(null);
$("#ddnsType").onchange = ddnsUpdateTargetHint;
$("#ddnsTaskClose").onclick = $("#ddnsTaskCancel").onclick = () => $("#ddnsTaskDialog").close();
$("#ddnsLogClose").onclick = () => $("#ddnsLogDialog").close();
$("#ddnsRows").onclick = (event) => { const button = event.target.closest("[data-ddns-action]"); if (button) ddnsAction(button); };
$("#ddnsCredentialForm").onsubmit = async (event) => {
  event.preventDefault();
  const accessKeyId = $("#ddnsAK").value.trim(), secretAccessKey = $("#ddnsSK").value.trim();
  try {
    await api("ddns/credentials", { method: "PUT", body: JSON.stringify({ accessKeyId, secretAccessKey }) });
    $("#ddnsCredentialForm").reset();
    toast("华为云密钥已保存");
    await ddnsRefresh();
  } catch (error) { if (error.status !== 401) toast(error.message); }
};
$("#ddnsTaskForm").onsubmit = async (event) => {
  event.preventDefault();
  const value = $("#ddnsNextRun").value;
  const nextRun = new Date(`${value}:00+08:00`);
  if (Number.isNaN(nextRun.getTime())) { toast("请选择有效的执行时间"); return; }
  const task = { domain: $("#ddnsDomain").value, type: $("#ddnsType").value, target: $("#ddnsTarget").value, nextRun: nextRun.toISOString(), repeat: $("#ddnsRepeat").value };
  try {
    await api(ddnsState.editing ? `ddns/tasks/${ddnsState.editing}` : "ddns/tasks", { method: ddnsState.editing ? "PUT" : "POST", body: JSON.stringify(task) });
    $("#ddnsTaskDialog").close();
    toast("任务已保存");
    await ddnsRefresh();
  } catch (error) { if (error.status !== 401) toast(error.message); }
};
setInterval(() => { if (!$("#ddnsPage").hidden) ddnsRefresh(); }, 15000);
