const input = document.querySelector("#file-input");
const form = input.form;
const urlInput = document.querySelector("#resource-url");
const fetchButton = document.querySelector("#fetch-url");
const summary = document.querySelector("#file-summary");
const count = document.querySelector("#file-count");
const list = document.querySelector("#file-list");
const submit = form.querySelector('button[type="submit"]');
const status = document.querySelector("#check-status");
const results = document.querySelector("#results");
let localFiles = [];
let remoteResources = [];
let isSubmitting = false;
const maxResources = 10;

function resourceEntries() {
  return [
    ...localFiles.map((file) => ({ file, sourceUrl: "" })),
    ...remoteResources,
  ];
}

function formatSize(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

function fileType(file) {
  if (file.type) return file.type;
  const extension = file.name.split(".").pop();
  return extension && extension !== file.name ? `${extension.toUpperCase()} file` : "Unknown file type";
}

input.addEventListener("change", () => {
  const previousEntries = resourceEntries();
  const available = Math.max(0, maxResources - resourceEntries().length);
  const addedFiles = Array.from(input.files).slice(0, available);
  localFiles = [...localFiles, ...addedFiles];
  if (input.files.length > available) {
    status.textContent = `Only ${maxResources} resources can be checked at once.`;
  }
  renderSelection(previousEntries);
});

fetchButton.addEventListener("click", async () => {
  if (isSubmitting) return;
  const resourceUrl = urlInput.value.trim();
  if (!resourceUrl) {
    urlInput.focus();
    status.textContent = "Enter a URL to fetch.";
    return;
  }
  if (resourceEntries().length >= maxResources) {
    status.textContent = `You can check up to ${maxResources} resources at once.`;
    return;
  }
  if (remoteResources.some((resource) => resource.sourceUrl === resourceUrl)) {
    status.textContent = "That URL has already been added.";
    return;
  }

  isSubmitting = true;
  updateSelectionCount();
  fetchButton.disabled = true;
  status.textContent = "Fetching and validating resource…";
  try {
    const response = await fetch("/api/fetch", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ url: resourceUrl }),
    });
    if (!response.ok) {
      let data = {};
      try {
        data = await response.json();
      } catch {
        // Use the standard message if the server did not return JSON.
      }
      throw new Error(data.error || "Could not fetch this resource.");
    }

    const blob = await response.blob();
    const previousEntries = resourceEntries();
    const name = response.headers.get("X-Resource-Name") || "resource";
    const file = new File([blob], name, { type: blob.type });
    remoteResources.push({ file, sourceUrl: resourceUrl });
    urlInput.value = "";
    renderSelection(previousEntries);
    status.textContent = `Fetched and validated ${name}. It is held in memory until you leave or reload this page.`;
  } catch (error) {
    status.textContent = error instanceof Error
      ? error.message
      : "Could not fetch this resource.";
  } finally {
    isSubmitting = false;
    fetchButton.disabled = false;
    updateSelectionCount();
  }
});

list.addEventListener("change", updateSelectionCount);

list.addEventListener("click", (event) => {
  const button = event.target.closest(".remove-resource");
  if (!button) return;

  const previousEntries = resourceEntries();
  const index = Number(button.dataset.resourceIndex);
  if (index < localFiles.length) {
    localFiles.splice(index, 1);
  } else {
    remoteResources.splice(index - localFiles.length, 1);
  }
  renderSelection(previousEntries);
});

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  if (isSubmitting) return;

  const entries = resourceEntries();
  const selected = Array.from(list.querySelectorAll('input[type="checkbox"]:checked'))
    .map((checkbox) => entries[Number(checkbox.dataset.resourceIndex)])
    .filter(Boolean);
  if (selected.length === 0) return;

  isSubmitting = true;
  updateSelectionCount();
  form.setAttribute("aria-busy", "true");
  status.textContent = "Checking resources…";
  results.replaceChildren();
  results.hidden = true;

  try {
    for (const [index, resource] of selected.entries()) {
      const { file, sourceUrl } = resource;
      status.textContent = `Checking resource ${index + 1} of ${selected.length}: ${file.name}`;
      const formData = new FormData();
      formData.append("file", file, file.name);

      try {
        const response = await fetch("/api/check", {
          method: "POST",
          body: formData,
        });
        let data = {};
        try {
          data = await response.json();
        } catch {
          // Keep the user-facing error clear if the server returns non-JSON.
        }
        if (!response.ok) {
          throw new Error(data.error || "Could not check this resource. Please try again.");
        }
        renderResult({ ...data, sourceUrl });
      } catch (error) {
        renderResult({
          file: file.name,
          sourceUrl,
          error: error instanceof Error
            ? error.message
            : "Could not check this resource. Please try again.",
        });
      }
    }
    status.textContent = `Finished checking ${selected.length} resource${selected.length === 1 ? "" : "s"}.`;
  } catch (error) {
    status.textContent = error instanceof Error
      ? error.message
      : "Could not check the resources. Please try again.";
  } finally {
    isSubmitting = false;
    form.removeAttribute("aria-busy");
    updateSelectionCount();
  }
});

function renderSelection(previousEntries = resourceEntries()) {
  const previouslyPresent = new Set(previousEntries.map(({ file }) => file));
  const previouslySelected = new Set(
    Array.from(list.querySelectorAll('input[type="checkbox"]:checked'))
      .map((checkbox) => previousEntries[Number(checkbox.dataset.resourceIndex)]?.file)
      .filter(Boolean),
  );
  const entries = resourceEntries();
  list.replaceChildren();
  if (entries.length === 0) {
    summary.hidden = true;
    updateSelectionCount();
    return;
  }

  for (const [index, resource] of entries.entries()) {
    const item = document.createElement("li");
    const row = document.createElement("div");
    const choice = document.createElement("label");
    const checkbox = document.createElement("input");
    const name = document.createElement("span");
    const meta = document.createElement("span");
    const remove = document.createElement("button");

    row.className = "file-row";
    choice.className = "file-choice";
    checkbox.type = "checkbox";
    checkbox.checked = !previouslyPresent.has(resource.file) || previouslySelected.has(resource.file);
    checkbox.dataset.resourceIndex = index;
    checkbox.setAttribute("aria-label", `Include ${resource.file.name} in check`);
    name.className = "file-name";
    name.textContent = resource.file.name;
    meta.className = "file-meta";
    meta.textContent = `${fileType(resource.file)} · ${formatSize(resource.file.size)}`;
    choice.append(checkbox, name, meta);
    remove.className = "remove-resource";
    remove.type = "button";
    remove.dataset.resourceIndex = index;
    remove.setAttribute("aria-label", `Remove ${resource.file.name}`);
    remove.textContent = "Remove";
    row.append(choice, remove);
    item.append(row);

    if (resource.sourceUrl) {
      const source = document.createElement("a");
      source.className = "file-source";
      source.href = resource.sourceUrl;
      source.target = "_blank";
      source.rel = "noopener noreferrer";
      source.textContent = resource.sourceUrl;
      item.append(source);
    }
    list.append(item);
  }
  summary.hidden = false;
  updateSelectionCount();
}

function renderResult(result) {
  const section = document.createElement("section");
  const heading = document.createElement("h2");
  section.className = "result";
  heading.textContent = result.file;
  section.append(heading);

  if (result.sourceUrl) {
    const source = document.createElement("p");
    const link = document.createElement("a");
    source.className = "file-source";
    link.href = result.sourceUrl;
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    link.textContent = result.sourceUrl;
    source.append(link);
    section.append(source);
  }

  if (result.error) {
    const error = document.createElement("p");
    error.className = "error";
    error.setAttribute("role", "alert");
    error.textContent = `Could not check this resource. ${result.error}`;
    section.append(error);
  } else {
    const outcome = document.createElement("p");
    outcome.className = `status ${result.detected ? "detected" : "clear"}`;
    outcome.textContent = result.detected
      ? "PROVENANCE SIGNAL DETECTED"
      : "PROVENANCE SIGNAL NOT DETECTED";
    section.append(outcome);

    const note = document.createElement("p");
    note.className = "note";
    note.textContent = "This is not a general-purpose AI detection verdict.";
    section.append(note);

    const details = document.createElement("ul");
    details.className = "details";
    for (const item of result.response?.results || []) {
      const detail = document.createElement("li");
      const parts = [`${item.type}: ${item.outcome}`];
      if (item.validation_state) parts.push(`validation: ${item.validation_state}`);
      if (item.issuer) parts.push(`issuer: ${item.issuer}`);
      if (item.model) parts.push(`model: ${item.model}`);
      if (item.generated_at) parts.push(`generated: ${item.generated_at}`);
      detail.textContent = parts.join(" · ");
      details.append(detail);
    }
    if (details.childElementCount > 0) section.append(details);
  }

  results.append(section);
  results.hidden = false;
}

function updateSelectionCount() {
  const entries = resourceEntries();
  const selectedCount = list.querySelectorAll('input[type="checkbox"]:checked').length;
  count.textContent = `${selectedCount} of ${entries.length} selected`;
  submit.disabled = isSubmitting || selectedCount === 0;
  fetchButton.disabled = isSubmitting || entries.length >= maxResources;
}

renderSelection();
