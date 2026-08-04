# README

## About

This is the official Wails React-TS template.

You can configure the project by editing `wails.json`. More information about the project settings can be found
here: https://wails.io/docs/reference/project-config

## Live Development

To run in live development mode, run `wails dev` in the project directory. This will run a Vite development
server that will provide very fast hot reload of your frontend changes. If you want to develop in a browser
and have access to your Go methods, there is also a dev server that runs on http://localhost:34115. Connect
to this in your browser, and you can call your Go code from devtools.

## Building

To build a redistributable, production mode package, use `wails build`.

## Features

- **System Theme Integration**: Automatically follows the OS dark/light mode in real-time. When mode is set to "Auto", the UI reacts instantly to OS appearance changes without requiring a restart. Users can also explicitly choose Light or Dark mode in Settings.
  - `useTheme` hook (`frontend/src/hooks/useTheme.ts`) provides shared theme state with a `prefers-color-scheme` media query listener.
  - `SystemAppearance()` Go method detects OS appearance at startup for correct initial window background.
  - macOS dark mode detection via NSUserDefaults (`appearance_darwin.go`).
