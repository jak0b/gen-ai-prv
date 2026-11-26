const input = document.querySelector("#file-input");
const form = input.form;
const summary = document.querySelector("#file-summary");
const count = document.querySelector("#file-count");
const list = document.querySelector("#file-list");
const submit = form.querySelector('button[type="submit"]');
const status = document.querySelector("#check-status");
const results = document.querySelector("#results");
let selectedFiles = [];
let isSubmitting = false;

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
  selectedFiles = Array.from(input.files);
  list.replaceChildren();

  if (selectedFiles.length === 0) {
    summary.hidden = true;
    submit.disabled = true;
    return;
  }

  for (const [index, file] of selectedFiles.entries()) {
    const item = document.createElement("li");
    const choice = document.createElement("label");
    const checkbox = document.createElement("input");
    const name = document.createElement("span");
    const meta = document.createElement("span");

    choice.className = "file-choice";
    checkbox.type = "checkbox";
    checkbox.checked = true;
    checkbox.dataset.fileIndex = index;
    checkbox.setAttribute("aria-label", `Include ${file.name} in check`);
    name.className = "file-name";
    name.textContent = file.name;
    meta.className = "file-meta";
    meta.textContent = `${fileType(file)} · ${formatSize(file.size)}`;
    choice.append(checkbox, name, meta);
    item.append(choice);
    list.append(item);
  }
  summary.hidden = false;
  updateSelectionCount();
});

list.addEventListener("change", updateSelectionCount);

form.addEventListener("formdata", (event) => {
  event.formData.delete(input.name);
  for (const checkbox of list.querySelectorAll('input[type="checkbox"]:checked')) {
    event.formData.append(input.name, selectedFiles[Number(checkbox.dataset.fileIndex)]);
  }
});

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  if (isSubmitting) return;

  const files = Array.from(list.querySelectorAll('input[type="checkbox"]:checked'))
    .map((checkbox) => selectedFiles[Number(checkbox.dataset.fileIndex)]);
  if (files.length === 0) return;

  isSubmitting = true;
  updateSelectionCount();
  form.setAttribute("aria-busy", "true");
  status.textContent = "Checking files…";
  results.replaceChildren();
  results.hidden = true;

  try {
    for (const [index, file] of files.entries()) {
      status.textContent = `Checking file ${index + 1} of ${files.length}: ${file.name}`;
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
          throw new Error(data.error || "Could not check this file. Please try again.");
        }
        renderResult(data);
      } catch (error) {
        renderResult({
          file: file.name,
          error: error instanceof Error
            ? error.message
            : "Could not check this file. Please try again.",
        });
      }
    }
    status.textContent = `Finished checking ${files.length} file${files.length === 1 ? "" : "s"}.`;
  } catch (error) {
    status.textContent = error instanceof Error
      ? error.message
      : "Could not check the files. Please try again.";
  } finally {
    isSubmitting = false;
    form.removeAttribute("aria-busy");
    updateSelectionCount();
  }
});

function renderResult(result) {
  const section = document.createElement("section");
  const heading = document.createElement("h2");
  section.className = "result";
  heading.textContent = result.file;
  section.append(heading);

  if (result.error) {
    const error = document.createElement("p");
    error.className = "error";
    error.setAttribute("role", "alert");
    error.textContent = `Could not check this file. ${result.error}`;
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
  const selectedCount = list.querySelectorAll('input[type="checkbox"]:checked').length;
  count.textContent = `${selectedCount} of ${selectedFiles.length} selected`;
  submit.disabled = isSubmitting || selectedCount === 0;
}
