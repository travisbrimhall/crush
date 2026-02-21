# UI Styling Changes

## Thinking Box

Enhanced the visual styling of the thinking/reasoning output in the terminal UI.

### Changes

**`internal/ui/styles/styles.go`**

1. Added `ThinkingIcon` constant (`🧠`)
2. Added `ThinkingHeader` style field for styling the header line
3. Updated `ThinkingBox` style with dotted border using `·` character, muted border color, and padding

**`internal/ui/chat/assistant.go`**

1. Added "🧠 Thinking" header to the thinking box content
2. Adjusted content width calculation to account for border and padding
3. Removed "Thought for Xs" duration footer

### Visual Result

```
·······························
·                             ·
·  🧠 Thinking                ·
·                             ·
·  [reasoning content here]   ·
·                             ·
·······························
```

---

## Attachments

Enhanced attachment display with full filenames and file-type icons.

### Changes

**`internal/ui/styles/styles.go`**

1. Added 10 file-type icon constants:
   - `🖼️` Image, `📄` Text, `💻` Code, `⚙️` Config, `📦` Archive
   - `🎵` Audio, `🎬` Video, `📕` PDF, `📊` Data, `📎` File (fallback)
2. Added corresponding style fields in `Attachments` struct

**`internal/ui/attachments/attachments.go`**

1. Removed `maxFilename` truncation - full filenames now display
2. Added `IconStyles` struct to hold all icon styles
3. Updated `icon()` function with extension-based icon selection (~70 extensions mapped)

**`internal/ui/model/ui.go`** and **`internal/ui/chat/messages.go`**

1. Updated `NewRenderer` calls to pass new `IconStyles` struct

### Visual Result

```
🖼️ screenshot.png  💻 main.go  ⚙️ config.yaml  📄 README.md
```
