-keep class io.flutter.** { *; }

# Play Core deferred-components refs are compile-only in this app (no
# Play Store split delivery); safe to drop from R8 analysis.
-dontwarn com.google.android.play.core.**
