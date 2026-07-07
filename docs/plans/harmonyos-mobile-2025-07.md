# HarmonyOS (鸿蒙) Mobile App — Flutter for OpenHarmony Adaptation Plan

## Date: 2025-07-10
## Status: Research Complete, Ready to Implement

## Executive Summary

ggcode mobile app can run on HarmonyOS with **minimal Dart code changes**. The core protocol (WebSocket + JSON + AES encryption) is 100% pure Dart and platform-agnostic. Only 6 platform plugins need adaptation, of which 5 already have official ohos versions.

## Architecture Compatibility

### What Works Zero-Change (Pure Dart — ~90% of codebase)

| Component | Why It Works |
|-----------|-------------|
| `connection_service.dart` (WebSocket + relay protocol) | Uses `dart:io` WebSocket — no platform channel |
| AES encryption (`cryptography` package) | Pure Dart implementation |
| JSON message handling / event replay | Pure Dart |
| Riverpod state management | Pure Dart |
| All UI screens (chat, connect, settings) | Flutter Material widgets |
| Markdown rendering (`flutter_markdown_plus`) | Pure Dart |
| Mermaid diagrams (`flutter_mermaid`) | Pure Dart |
| Syntax highlighting (`highlight` + `flutter_highlight`) | Pure Dart |
| `uuid`, `path`, `google_fonts`, `animations` | Pure Dart |

### Plugin Compatibility Matrix

| Plugin | Version | ohos Support | Adaptation Needed |
|--------|---------|-------------|-------------------|
| `shared_preferences` | ^2.2.2 | **Official** (v2.5.4) | None — use git ref |
| `path_provider` | ^2.1.5 | **Official** (v2.1.5) | None — use git ref |
| `url_launcher` | ^6.2.2 | **Official** (v6.3.2) | None — use git ref |
| `image_picker` | ^1.1.2 | **Official** (v1.2.1) | None — use git ref |
| `sqlite3` + `sqlite3_flutter_libs` | ^3.3.2 | **Community** (SageMik/sqlite3-ohos.dart) | Swap to ohos fork |
| `wakelock_plus` | ^1.6.0 | **No ohos version** | Write custom ohos plugin |
| `mobile_scanner` | ^7.0.0 | **No ohos version** | Write custom ohos plugin OR replace |

**Score: 5/7 plugins have ready ohos versions. Only 2 need custom work.**

---

## Implementation Steps

### Phase 0: Environment Setup (Day 1)

1. **Install DevEco Studio** (HarmonyOS IDE)
   - Download from https://developer.harmonyos.com/cn/develop/dedeco-studio
   - Configure HarmonyOS SDK + emulator or physical device

2. **Install Flutter for OpenHarmony**
   - Repo: https://atomgit.com/openharmony-tpc/flutter_flutter
   - Use FVM to manage multiple Flutter SDK versions:
     ```bash
     fvm custom ohos-flutter --path /path/to/flutter_flutter
     ```
   - Verify: `flutter doctor` should show ohos support
   - Verify: `flutter devices` should show HarmonyOS device/emulator

### Phase 1: Generate ohos Project (Day 1)

```bash
cd mobile/flutter

# Generate ohos platform directory (does NOT modify existing android/ios code)
flutter create --platforms ohos .

# Verify structure
ls ohos/
# entry/  build-profile.json5  hvigorfile.ts  oh-package.json5
```

### Phase 2: Swap Dependencies for ohos Versions (Day 2)

Update `pubspec.yaml` to use ohos-adapted plugin versions:

```yaml
dependencies:
  flutter:
    sdk: flutter

  # --- Platform plugins with ohos official support ---
  shared_preferences:
    git:
      url: https://gitcode.com/openharmony-tpc/flutter_packages.git
      path: packages/shared_preferences/shared_preferences
      ref: br_shared_preferences-v2.5.4_ohos

  path_provider:
    git:
      url: https://gitcode.com/openharmony-tpc/flutter_packages.git
      path: packages/path_provider/path_provider
      ref: br_path_provider-v2.1.5_ohos

  url_launcher:
    git:
      url: https://gitcode.com/openharmony-tpc/flutter_packages.git
      path: packages/url_launcher/url_launcher
      ref: br_url_launcher-v6.3.2_ohos

  image_picker:
    git:
      url: https://gitcode.com/openharmony-tpc/flutter_packages.git
      path: packages/image_picker/image_picker
      ref: br_image_picker-v1.2.1_ohos

  # --- SQLite: use community ohos fork ---
  sqlite3:
    git:
      url: https://github.com/SageMik/sqlite3-ohos.dart
      path: sqlite3
  sqlite3_flutter_libs:
    git:
      url: https://github.com/SageMik/sqlite3-ohos.dart
      path: sqlite3_flutter_libs

  # --- Pure Dart deps (no change needed) ---
  flutter_riverpod: ^3.0.0
  web_socket_channel: ^3.0.0
  flutter_markdown_plus: ^1.0.0
  highlight: ^0.7.0
  flutter_highlight: ^0.7.0
  uuid: ^4.2.3
  google_fonts: ^8.0.0
  animations: ^2.0.8
  flutter_mermaid: ^0.1.0
  cryptography: ^2.9.0
  path: ^1.9.1

  # --- Plugins needing custom ohos adaptation ---
  # wakelock_plus: see Phase 3
  # mobile_scanner: see Phase 3
```

**Note:** Conditional dependency management is needed. Two approaches:
- **Option A (recommended):** Separate `pubspec.ohos.yaml` overlay
- **Option B:** Use `dependency_overrides` in a separate build config

### Phase 3: Write Custom ohos Plugins (Day 3-5)

#### 3a: wakelock_plus ohos plugin

**What it does:** Prevents screen sleep during agent streaming.

**ohos API:** `@ohos.runningLock` (RunningLockType.PROXIMITY_SCREEN_CONTROL)

**Implementation:**

```
mobile/flutter/ohos_plugins/wakelock_plus_ohos/
├── pubspec.yaml
├── lib/
│   └── wakelock_plus_ohos.dart
└── ohos/
    └── src/main/ets/
        ├── index.ets
        └── WakelockPlugin.ets
```

ArkTS side:
```typescript
import runningLock from '@ohos.runningLock';

export default class WakelockPlugin {
  private lock: runningLock.RunningLock | null = null;

  enable(): void {
    this.lock = runningLock.create('ggcode_wakelock',
      runningLock.RunningLockType.PROXIMITY_SCREEN_CONTROL);
    this.lock.hold(600000); // 10 min timeout
  }

  disable(): void {
    if (this.lock) {
      this.lock.release();
      this.lock = null;
    }
  }
}
```

Dart side:
```dart
// wakelock_plus_ohos.dart
import 'dart:io';
import 'package:flutter/services.dart';

class WakelockPlusOhos {
  static const _channel = MethodChannel('wakelock_plus_ohos');

  static Future<void> enable() async {
    if (Platform.operatingSystem == 'ohos') {
      await _channel.invokeMethod('enable');
    }
  }

  static Future<void> disable() async {
    if (Platform.operatingSystem == 'ohos') {
      await _channel.invokeMethod('disable');
    }
  }
}
```

#### 3b: mobile_scanner replacement

**Options (pick one):**

1. **Use ohos camera + scanKit** (recommended)
   - Huawei Scan Kit provides QR/barcode scanning
   - ohos API: `@ohos.scankit` (scanBarcode)
   - Write a thin Flutter platform channel wrapper

2. **Replace with `image_picker` + software QR decode**
   - Use `image_picker` (already has ohos support) to capture photo
   - Decode QR in pure Dart using `zxing` library
   - Slower but simpler

3. **Simplify: text-based connection code**
   - Instead of QR scan, let user paste/type the share URL
   - Zero platform dependency
   - Can be the MVP approach, add scanner later

**Recommended:** Start with Option 3 (text input) for MVP, add Scan Kit later.

### Phase 4: Build & Test (Day 5-7)

1. **Configure signing in DevEco Studio:**
   - Open `mobile/flutter/ohos/` in DevEco Studio
   - File > Project Structure > Signing Configs
   - Enable "Automatically generate signature"

2. **Build:**
   ```bash
   cd mobile/flutter
   flutter build hap --release  # HarmonyOS APP Package
   ```

3. **Test on device/emulator:**
   ```bash
   flutter run -d <ohos-device-id>
   ```

4. **Key test scenarios:**
   - [ ] App launches without crash
   - [ ] Can paste share URL and connect to relay
   - [ ] WebSocket connection establishes
   - [ ] Chat messages render (markdown, code blocks)
   - [ ] AES decryption works (messages readable)
   - [ ] Reconnection works after network drop
   - [ ] Screen wakelock during streaming

### Phase 5: Polish & CI (Day 7-10)

1. **App icon and splash screen** for HarmonyOS
2. **Add to `.github/workflows/mobile-release.yml`:**
   ```yaml
   build-ohos:
     runs-on: macos-latest
     steps:
       - uses: actions/checkout@v4
       - name: Setup Ohos Flutter
         run: ...  # Install flutter_flutter ohos branch
       - name: Build HAP
         run: cd mobile/flutter && flutter build hap --release
       - name: Upload artifact
         uses: actions/upload-artifact@v4
         with:
           name: ggcode-mobile-ohos
           path: mobile/flutter/build/ohos/outputs/*.hap
   ```

3. **Update `Makefile`:**
   ```makefile
   .PHONY: build-ohos
   build-ohos:
       cd mobile/flutter && flutter build hap --release
   ```

4. **Update `version_sync.sh`** to include ohos build number

---

## Key Decisions

### Decision 1: FVM-based SDK Management
Use Flutter Version Manager (FVM) to maintain two Flutter SDKs:
- Standard Flutter (for Android/iOS builds)
- OpenHarmony Flutter fork (for HarmonyOS builds)

This avoids polluting the existing build pipeline.

### Decision 2: Conditional Dependencies
The ohos pubspec uses git refs for ohos-adapted plugins while the standard pubspec stays unchanged. This means:
- `pubspec.yaml` — unchanged (Android/iOS)
- Build scripts switch pubspec before building ohos

Alternative: use a unified pubspec with `dependency_overrides` that the ohos Flutter SDK resolves differently.

### Decision 3: MVP Without QR Scanner
For initial release, skip `mobile_scanner` entirely. Users paste the share URL manually. Add Huawei Scan Kit integration in v2.

---

## Risk Assessment

| Risk | Probability | Impact | Mitigation |
|------|------------|--------|------------|
| `dart:io` WebSocket not supported on ohos | Low | Critical | Test early in Phase 1 |
| sqlite3 ohos fork has bugs | Medium | Medium | Fallback: use `shared_preferences` for MVP |
| DevEco Studio is macOS/Windows only | Low | Low | CI uses macos runner |
| Flutter ohos SDK lags behind upstream | Medium | Low | Pin to a known-good version |
| `cryptography` package FFI issues | Low | High | Test AES specifically in Phase 1 |
| HarmonyOS signing complexity | Medium | Low | DevEco auto-sign for dev, manual for release |

---

## References

- [OpenHarmony-TPC/flutter_flutter](https://atomgit.com/openharmony-tpc/flutter_flutter) — Flutter SDK fork
- [OpenHarmony-TPC/flutter_packages](https://github.com/OpenHarmony-TPC/flutter_packages) — Adapted plugins
- [SageMik/sqlite3-ohos.dart](https://github.com/SageMik/sqlite3-ohos.dart) — SQLite ohos fork
- [CPF-Flutter/flutter_samples](https://gitcode.com/CPF-Flutter/flutter_samples) — Plugin adaptation guide
- [DevEco Studio](https://developer.harmonyos.com/cn/develop/dedeco-studio) — HarmonyOS IDE
- [Adaptation tutorial (woshipm.com)](https://www.woshipm.com/share/6348869.html) — Step-by-step guide
