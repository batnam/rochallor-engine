# Workflow Modeller — Web and Desktop

Workflow Modeller lets you draw a workflow, edit its steps, check for errors, and
save it as JSON for the Rochallor Workflow Engine.

You can use it in a **web browser** or as a **desktop application** on Windows,
macOS, and Linux. Both versions share the same editor and workflow format, so
you can move a workflow between them without converting it.

## Choose how to use it

| | Web | Desktop |
|---|---|---|
| Open the editor | Visit the modeller URL in your browser | Launch Rochallor Workflow Modeller |
| Install on your computer | No app installation needed | Install a package for your OS |
| Open a workflow | Choose a JSON file or paste JSON | Choose a JSON file using the system file picker, or paste JSON |
| Save a workflow | Download a JSON file | Choose a file name and location in the system Save As dialog |
| Drafts | Stored in that browser's local storage | Stored in the desktop app's local storage |
| Connect to the engine | Set the engine URL in the editor | Set the engine URL in the editor |

You can keep using the web version after installing the desktop app. Both can
connect to the same engine, but their local drafts and settings are separate.

## Open the web version

Follow the [Docker Compose quick start](getting-started.md#quick-start-docker-compose-no-clone-required),
then open **http://localhost:13000**. If your team hosts the modeller, use the URL
they provide instead.

To run the web editor from source, see [Modeller development](development.md#modeller-development-web-and-desktop).

## Get the desktop version

Desktop packages are currently produced by the **Modeller Web and Desktop**
GitHub Actions workflow. After a successful build, download the package for your
OS and CPU architecture from the run's **Artifacts** section. See
[Desktop Modeller builds](release.md#desktop-modeller-builds) for the artifact names.

| OS | Package | How to open it |
|---|---|---|
| macOS | `.dmg` | Open the disk image, copy the app to Applications, then launch it |
| Windows | `.exe` or `.msi` | Run one installer, then open the app from the Start menu |
| Linux | `.deb` or `.AppImage` | Install the Debian package with your package manager, or make the AppImage executable and run it |

These are unsigned development builds; automated publication to GitHub Releases
is not configured yet. You can also [build the desktop app locally](development.md#build-the-modeller).

You do not need Node.js or Rust to use a packaged app. The desktop app includes
the editor, while the workflow engine runs separately.

## Create or open a workflow

The steps below are the same in web and desktop.

1. Click **New Workflow**, or open an existing file with **Import Workflow** →
   **Choose JSON file** → **Import**. You can also paste JSON into the import dialog.
2. Drag steps from the palette onto the canvas and connect them.
3. Select a step to edit its properties in the panel on the right.
4. Click **Validate Workflow** and fix any reported errors.
5. Click **Export Workflow**, keep **Include canvas layout** checked, then click
   **Save JSON**. This saves the workflow together with its node positions.

Import replaces the current canvas. Export your work or save a draft before
opening another workflow. Export and upload are disabled while validation errors
remain. You can still save unfinished work as a draft.

## Save drafts and move between versions

For a named snapshot, click **Save / Load Drafts Workflows**, enter a name, and
click **Save draft**. Open the same dialog and click **Restore** to return to it.

Drafts stay in the app or browser profile where you saved them. They are not
automatically shared between web and desktop, and they do not include engine settings.

To move work from one version to the other:

1. In the first version, choose **Export Workflow** → **Save JSON**. Keep
   **Include canvas layout** checked to preserve node positions.
2. In the other version, choose **Import Workflow** and import that JSON file.

The same process works when moving to another computer. **Copy to clipboard**
in the export dialog is another way to transfer the JSON.

On desktop, **Save JSON** opens Save As each time. Editing an imported workflow
does not automatically update its original file. Save drafts for work in progress
and export JSON when you need a file to keep or share.

## Connect to a workflow engine

You can draw, edit, validate, and save local workflows without a running engine.
The desktop editor also works without an internet connection. To load definitions
from an engine, upload them, or execute workflows, start an engine separately.

1. Open **Engine Settings**.
2. Enter the **Engine base URL** using the table below. Enter the server address
   without `/v1/definitions`.
3. If your deployment requires authentication, enter its full **Authorization
   header**, for example `Bearer …`. Otherwise leave it blank.
4. Click **Test connection**, then **Save**.
5. Use **Load Workflow from engine** to open a definition, or **Upload Workflow
   to Engine** to publish the current definition.

| Engine location | Base URL |
|---|---|
| Docker Compose quick start on your computer | `http://localhost:18080` |
| Engine started from source with default ports | `http://localhost:8080` |
| A server hosted by your team | The HTTP(S) engine address and port they provide |

Uploading saves a definition in the engine. To start an instance and run workers,
follow the [Getting Started guide](getting-started.md#6-run-a-worker).

## Common questions

**Why are my web drafts missing in the desktop app?**

Each version has its own local storage. Use JSON export and import to transfer
the workflow. Configure the engine URL separately in each version.

**Why can I edit a workflow but cannot load or upload one?**

Local editing does not need the engine. Check that the engine is running and
reachable, then use **Test connection**. The modeller URL (`:13000` in the Docker
quick start) and the engine URL (`:18080`) are different.

**Where did Save JSON put my file?**

On web, check your browser's downloads. On desktop, check the location you chose
in Save As. Cancelling that dialog leaves the file unsaved.

For the JSON structure, see [Workflow Format](workflow-format.md). For source
setup and build commands, see [Modeller development](development.md#modeller-development-web-and-desktop).
