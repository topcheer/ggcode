// Wakelock implementation for platforms that have wakelock_plus (Android/iOS/macOS).

import 'package:wakelock_plus/wakelock_plus.dart';

void wakelockEnable() => WakelockPlus.enable();
void wakelockDisable() => WakelockPlus.disable();
