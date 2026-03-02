// Get all charmtone colors once from computed styles
const rootStyles = getComputedStyle(document.documentElement);
const colors = {
  charple: rootStyles.getPropertyValue("--charple").trim(),
  cherry: rootStyles.getPropertyValue("--cherry").trim(),
  julep: rootStyles.getPropertyValue("--julep").trim(),
  urchin: rootStyles.getPropertyValue("--urchin").trim(),
  butter: rootStyles.getPropertyValue("--butter").trim(),
  squid: rootStyles.getPropertyValue("--squid").trim(),
  pepper: rootStyles.getPropertyValue("--pepper").trim(),
  tuna: rootStyles.getPropertyValue("--tuna").trim(),
  uni: rootStyles.getPropertyValue("--uni").trim(),
  coral: rootStyles.getPropertyValue("--coral").trim(),
  violet: rootStyles.getPropertyValue("--violet").trim(),
  malibu: rootStyles.getPropertyValue("--malibu").trim(),
};

const easeDuration = 500;
const easeType = "easeOutQuart";

// Cache toggle state
let includeCache = true;

// Helper to get effective input tokens based on toggle
function getEffectiveInputTokens(data) {
  if (includeCache) {
    return data.total_prompt_tokens || data.prompt_tokens || 0;
  }
  // Use input_tokens if available (new sessions), otherwise fall back to prompt_tokens (legacy)
  return data.total_input_tokens || data.input_tokens || data.total_prompt_tokens || data.prompt_tokens || 0;
}

function getEffectiveTotalTokens(data) {
  const input = getEffectiveInputTokens(data);
  const output = data.total_completion_tokens || data.completion_tokens || 0;
  return input + output;
}

// Helper functions
function formatNumber(n) {
  return new Intl.NumberFormat().format(Math.round(n));
}

function formatCompact(n) {
  if (n >= 1000000) return (n / 1000000).toFixed(1) + "M";
  if (n >= 1000) return (n / 1000).toFixed(1) + "k";
  return Math.round(n).toString();
}

function formatCost(n) {
  return "$" + n.toFixed(2);
}

function formatTime(ms) {
  if (ms < 1000) return Math.round(ms) + "ms";
  return (ms / 1000).toFixed(1) + "s";
}

const charpleColor = { r: 107, g: 80, b: 255 };
const tunaColor = { r: 255, g: 109, b: 170 };

function interpolateColor(ratio, alpha = 1) {
  const r = Math.round(charpleColor.r + (tunaColor.r - charpleColor.r) * ratio);
  const g = Math.round(charpleColor.g + (tunaColor.g - charpleColor.g) * ratio);
  const b = Math.round(charpleColor.b + (tunaColor.b - charpleColor.b) * ratio);
  if (alpha < 1) {
    return `rgba(${r}, ${g}, ${b}, ${alpha})`;
  }
  return `rgb(${r}, ${g}, ${b})`;
}

function getTopItemsWithOthers(items, countKey, labelKey, topN = 10) {
  const topItems = items.slice(0, topN);
  const otherItems = items.slice(topN);
  const otherCount = otherItems.reduce((sum, item) => sum + item[countKey], 0);
  const displayItems = [...topItems];
  if (otherItems.length > 0) {
    const otherItem = { [countKey]: otherCount, [labelKey]: "others" };
    displayItems.push(otherItem);
  }
  return displayItems;
}

// Populate summary cards
document.getElementById("total-sessions").textContent = formatNumber(
  stats.total.total_sessions,
);
document.getElementById("total-messages").textContent = formatCompact(
  stats.total.total_messages,
);

function updateTotalTokensCard() {
  document.getElementById("total-tokens").textContent = formatCompact(
    getEffectiveTotalTokens(stats.total),
  );
}
updateTotalTokensCard();

document.getElementById("total-cost").textContent = formatCost(
  stats.total.total_cost,
);
document.getElementById("avg-tokens").innerHTML =
  '<span title="Average">x̅</span> ' +
  formatCompact(stats.total.avg_tokens_per_session);
document.getElementById("avg-response").innerHTML =
  '<span title="Average">x̅</span> ' + formatTime(stats.avg_response_time_ms);

// Chart defaults
Chart.defaults.color = colors.squid;
Chart.defaults.borderColor = colors.squid;

if (stats.recent_activity?.length > 0) {
  new Chart(document.getElementById("recentActivityChart"), {
    type: "bar",
    data: {
      labels: stats.recent_activity.map((d) => d.day),
      datasets: [
        {
          label: "Sessions",
          data: stats.recent_activity.map((d) => d.session_count),
          backgroundColor: colors.charple,
          borderRadius: 4,
          yAxisID: "y",
        },
        {
          label: "Tokens (K)",
          data: stats.recent_activity.map((d) => d.total_tokens / 1000),
          backgroundColor: colors.julep,
          borderRadius: 4,
          yAxisID: "y1",
        },
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: { duration: 800, easing: easeType },
      interaction: { mode: "index", intersect: false },
      scales: {
        y: { position: "left", title: { display: true, text: "Sessions" } },
        y1: {
          position: "right",
          title: { display: true, text: "Tokens (K)" },
          grid: { drawOnChartArea: false },
        },
      },
    },
  });
}

// Heatmap (Hour × Day of Week) - Bubble Chart
const dayLabels = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

// Populate heatmap date range
if (stats.heatmap_date_range?.first_day && stats.heatmap_date_range?.last_day) {
  const range = stats.heatmap_date_range;
  const formatShortDate = (dateStr) => {
    const date = new Date(dateStr + "T00:00:00");
    const month = date.toLocaleDateString("en-US", { month: "short" });
    const day = date.getDate();
    return `${month} ${day}`;
  };
  document.getElementById("heatmap-range").textContent =
    `(${formatShortDate(range.first_day)} to ${formatShortDate(range.last_day)}, ${range.total_days} days)`;
}

// Convert hour to "restaurant time" (5am = 0, 4am = 23)
function toRestaurantHour(hour) {
  return (hour - 5 + 24) % 24;
}

function fromRestaurantHour(rh) {
  return (rh + 5) % 24;
}

// Token Heatmap
function getHeatmapTokens(h) {
  if (includeCache) {
    return h.total_tokens || 0;
  }
  // Use excl_cache if available (new data), otherwise fall back to total (legacy)
  return h.total_tokens_excl_cache || h.total_tokens || 0;
}

function getMaxHeatmapTokens() {
  if (!stats.hour_day_token_heatmap?.length) return 1;
  return Math.max(...stats.hour_day_token_heatmap.map((h) => getHeatmapTokens(h))) || 1;
}

let maxTokens = getMaxHeatmapTokens();
let tokenScaleFactor = 20 / Math.sqrt(maxTokens);
let tokenHeatmapChart = null;

if (stats.hour_day_token_heatmap?.length > 0) {
  tokenHeatmapChart = new Chart(document.getElementById("tokenHeatmapChart"), {
    type: "bubble",
    data: {
      datasets: [
        {
          label: "Tokens",
          data: stats.hour_day_token_heatmap
            .filter((h) => getHeatmapTokens(h) > 0)
            .map((h) => ({
              x: h.day_of_week,
              y: toRestaurantHour(h.hour),
              hour: h.hour,
              r: Math.sqrt(getHeatmapTokens(h)) * tokenScaleFactor,
              tokens: getHeatmapTokens(h),
            })),
          backgroundColor: (ctx) => {
            const tokens =
              ctx.raw?.tokens || ctx.dataset.data[ctx.dataIndex]?.tokens || 0;
            const ratio = tokens / maxTokens;
            return interpolateColor(ratio);
          },
          borderWidth: 0,
        },
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      scales: {
        x: {
          min: -0.5,
          max: 6.5,
          grid: { display: false },
          title: { display: true, text: "Day of Week" },
          ticks: {
            stepSize: 1,
            autoSkip: false,
            callback: (v) => (v >= 0 && v <= 6 && Number.isInteger(v) ? dayLabels[v] : null),
          },
          afterBuildTicks: (axis) => {
            axis.ticks = [0, 1, 2, 3, 4, 5, 6].map((v) => ({ value: v }));
          },
        },
        y: {
          min: -0.5,
          max: 23.5,
          reverse: false,
          grid: { display: false },
          title: { display: true, text: "Hour of Day" },
          ticks: {
            stepSize: 1,
            autoSkip: false,
            callback: (v) => (v >= 0 && v <= 23 && Number.isInteger(v) ? fromRestaurantHour(v) : null),
          },
          afterBuildTicks: (axis) => {
            axis.ticks = Array.from({ length: 24 }, (_, i) => ({ value: i }));
          },
        },
      },
      plugins: {
        legend: { display: false },
        tooltip: {
          callbacks: {
            label: (ctx) => {
              const tokens = ctx.raw.tokens;
              const formatted = tokens >= 1000000
                ? `${(tokens / 1000000).toFixed(1)}M`
                : tokens >= 1000
                  ? `${(tokens / 1000).toFixed(0)}K`
                  : tokens;
              return `${dayLabels[ctx.raw.x]} ${ctx.raw.hour}:00 - ${formatted} tokens`;
            },
          },
        },
      },
    },
  });
}

function updateTokenHeatmap() {
  if (!tokenHeatmapChart) return;
  maxTokens = getMaxHeatmapTokens();
  tokenScaleFactor = 20 / Math.sqrt(maxTokens);
  tokenHeatmapChart.data.datasets[0].data = stats.hour_day_token_heatmap
    .filter((h) => getHeatmapTokens(h) > 0)
    .map((h) => ({
      x: h.day_of_week,
      y: toRestaurantHour(h.hour),
      hour: h.hour,
      r: Math.sqrt(getHeatmapTokens(h)) * tokenScaleFactor,
      tokens: getHeatmapTokens(h),
    }));
  tokenHeatmapChart.update();
}

if (stats.tool_usage?.length > 0) {
  // Tool calls chart (sorted by call count)
  const callsTools = getTopItemsWithOthers(
    [...stats.tool_usage].sort((a, b) => b.call_count - a.call_count),
    "call_count",
    "tool_name",
  );
  const maxCalls = Math.max(...callsTools.map((t) => t.call_count));
  new Chart(document.getElementById("toolChart"), {
    type: "bar",
    data: {
      labels: callsTools.map((t) => t.tool_name),
      datasets: [
        {
          label: "Calls",
          data: callsTools.map((t) => t.call_count),
          backgroundColor: (ctx) => {
            const value = ctx.raw;
            const ratio = value / maxCalls;
            return interpolateColor(ratio);
          },
          borderRadius: 4,
        },
      ],
    },
    options: {
      indexAxis: "y",
      responsive: true,
      maintainAspectRatio: false,
      animation: { duration: easeDuration, easing: easeType },
      plugins: { legend: { display: false } },
    },
  });

  // Tool tokens chart (sorted by estimated tokens)
  const tokenTools = getTopItemsWithOthers(
    [...stats.tool_usage].sort((a, b) => b.estimated_tokens - a.estimated_tokens),
    "estimated_tokens",
    "tool_name",
  );
  const maxTokens = Math.max(...tokenTools.map((t) => t.estimated_tokens));
  new Chart(document.getElementById("toolTokenChart"), {
    type: "bar",
    data: {
      labels: tokenTools.map((t) => t.tool_name),
      datasets: [
        {
          label: "Estimated Tokens",
          data: tokenTools.map((t) => t.estimated_tokens),
          backgroundColor: (ctx) => {
            const value = ctx.raw;
            const ratio = value / maxTokens;
            return interpolateColor(ratio);
          },
          borderRadius: 4,
        },
      ],
    },
    options: {
      indexAxis: "y",
      responsive: true,
      maintainAspectRatio: false,
      animation: { duration: easeDuration, easing: easeType },
      plugins: {
        legend: { display: false },
        tooltip: {
          callbacks: {
            label: (ctx) => {
              const tokens = ctx.raw;
              if (tokens >= 1000000) return `${(tokens / 1000000).toFixed(1)}M tokens`;
              if (tokens >= 1000) return `${(tokens / 1000).toFixed(0)}K tokens`;
              return `${tokens} tokens`;
            },
          },
        },
      },
    },
  });

  // Tool tokens per call chart (sorted by tokens/call ratio)
  const tokensPerCallTools = [...stats.tool_usage]
    .map((t) => ({
      ...t,
      tokens_per_call: t.call_count > 0 ? Math.round(t.estimated_tokens / t.call_count) : 0,
    }))
    .sort((a, b) => b.tokens_per_call - a.tokens_per_call);
  const displayTpcTools = getTopItemsWithOthers(
    tokensPerCallTools,
    "tokens_per_call",
    "tool_name",
  );
  const maxTpc = Math.max(...displayTpcTools.map((t) => t.tokens_per_call));
  new Chart(document.getElementById("toolTokensPerCallChart"), {
    type: "bar",
    data: {
      labels: displayTpcTools.map((t) => t.tool_name),
      datasets: [
        {
          label: "Tokens/Call",
          data: displayTpcTools.map((t) => t.tokens_per_call),
          backgroundColor: (ctx) => {
            const value = ctx.raw;
            const ratio = value / maxTpc;
            return interpolateColor(ratio);
          },
          borderRadius: 4,
        },
      ],
    },
    options: {
      indexAxis: "y",
      responsive: true,
      maintainAspectRatio: false,
      animation: { duration: easeDuration, easing: easeType },
      plugins: {
        legend: { display: false },
        tooltip: {
          callbacks: {
            label: (ctx) => {
              const tpc = ctx.raw;
              if (tpc >= 1000) return `${(tpc / 1000).toFixed(1)}K tokens/call`;
              return `${tpc} tokens/call`;
            },
          },
        },
      },
    },
  });
}

// Token Distribution Pie
const tokenPieChart = new Chart(document.getElementById("tokenPieChart"), {
  type: "doughnut",
  data: {
    labels: ["Input Tokens (incl. cache)", "Output Tokens"],
    datasets: [
      {
        data: [
          stats.total.total_prompt_tokens,
          stats.total.total_completion_tokens,
        ],
        backgroundColor: [colors.charple, colors.julep],
        borderWidth: 0,
      },
    ],
  },
  options: {
    responsive: true,
    maintainAspectRatio: false,
    animation: { duration: easeDuration, easing: easeType },
    plugins: {
      legend: { position: "bottom" },
    },
  },
});

function updateTokenPieChart() {
  const inputLabel = includeCache ? "Input Tokens (incl. cache)" : "Input Tokens (excl. cache)";
  tokenPieChart.data.labels[0] = inputLabel;
  tokenPieChart.data.datasets[0].data[0] = getEffectiveInputTokens(stats.total);
  tokenPieChart.update();
}

// Model Usage Chart (horizontal bar)
if (stats.usage_by_model?.length > 0) {
  const displayModels = getTopItemsWithOthers(
    stats.usage_by_model,
    "message_count",
    "model",
  );
  const maxModelValue = Math.max(...displayModels.map((m) => m.message_count));
  new Chart(document.getElementById("modelChart"), {
    type: "bar",
    data: {
      labels: displayModels.map((m) =>
        m.provider ? `${m.model} (${m.provider})` : m.model,
      ),
      datasets: [
        {
          label: "Messages",
          data: displayModels.map((m) => m.message_count),
          backgroundColor: (ctx) => {
            const value = ctx.raw;
            const ratio = value / maxModelValue;
            return interpolateColor(ratio);
          },
          borderRadius: 4,
        },
      ],
    },
    options: {
      indexAxis: "y",
      responsive: true,
      maintainAspectRatio: false,
      animation: { duration: easeDuration, easing: easeType },
      plugins: { legend: { display: false } },
    },
  });
}

if (stats.usage_by_model?.length > 0) {
  const providerData = stats.usage_by_model.reduce((acc, m) => {
    acc[m.provider] = (acc[m.provider] || 0) + m.message_count;
    return acc;
  }, {});
  const providerColors = [
    colors.malibu,
    colors.charple,
    colors.violet,
    colors.tuna,
    colors.coral,
    colors.uni,
  ];
  new Chart(document.getElementById("providerPieChart"), {
    type: "doughnut",
    data: {
      labels: Object.keys(providerData),
      datasets: [
        {
          data: Object.values(providerData),
          backgroundColor: Object.keys(providerData).map(
            (_, i) => providerColors[i % providerColors.length],
          ),
          borderWidth: 0,
        },
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: { duration: easeDuration, easing: easeType },
      plugins: {
        legend: { position: "bottom" },
      },
    },
  });
}

// Daily Usage Table
const tableBody = document.querySelector("#daily-table tbody");

function formatDateWithDay(dateStr) {
  const date = new Date(dateStr + "T00:00:00");
  const dayName = date.toLocaleDateString("en-US", { weekday: "short" });
  const month = date.toLocaleDateString("en-US", { month: "short" });
  const day = date.getDate();
  return `${dayName} ${month} ${day}`;
}

function getDailyInputTokens(d) {
  if (includeCache) {
    return d.prompt_tokens || 0;
  }
  return d.input_tokens || d.prompt_tokens || 0;
}

function renderDailyTable() {
  tableBody.innerHTML = "";
  if (stats.usage_by_day?.length > 0) {
    const fragment = document.createDocumentFragment();
    stats.usage_by_day.slice(0, 30).forEach((d) => {
      const row = document.createElement("tr");
      const inputTokens = getDailyInputTokens(d);
      const totalTokens = inputTokens + (d.completion_tokens || 0);
      row.innerHTML = `<td>${formatDateWithDay(d.day)}</td><td>${d.session_count}</td><td>${formatNumber(
        inputTokens,
      )}</td><td>${formatNumber(
        d.completion_tokens,
      )}</td><td>${formatNumber(totalTokens)}</td><td>${formatCost(
        d.cost,
      )}</td>`;
      fragment.appendChild(row);
    });
    tableBody.appendChild(fragment);
  }
}
renderDailyTable();

// Toggle event handler
document.getElementById("includeCache").addEventListener("change", (e) => {
  includeCache = e.target.checked;
  updateTotalTokensCard();
  updateTokenPieChart();
  updateTokenHeatmap();
  renderDailyTable();
});
