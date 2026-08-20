"""Focused regression coverage for the Windows renderer patcher.

The fixture checks the fail-closed exact-anchor behavior without requiring a
locally installed Store package. Set CODEX_MUX_WINDOWS_RENDERER_DIR to an
unpacked supported `webview/assets` directory to additionally test that exact
renderer build before a release.
"""

from __future__ import annotations

import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from patch_windows import (
    CREATE_SHORTCUT_SCRIPT,
    CURRENT_RENDERER_PROFILE,
    LOGIN_MAIN_TRAILER_ANCHOR,
    LOGIN_PRELOAD_TRAILER_ANCHOR,
    PLUGIN_PICKER_ANCHOR,
    PLUGIN_PICKER_REPLACEMENT,
    PLUGIN_REQUEST_ANCHOR,
    PLUGIN_REQUEST_REPLACEMENT,
    PROFILE_PICKER_ANCHOR,
    PROFILE_PICKER_REPLACEMENT,
    PROFILE_QUERY_ANCHOR,
    PROFILE_QUERY_REPLACEMENT,
    RESET_HEADER_ANCHOR,
    RESET_HEADER_REPLACEMENT,
    RESET_MUTATION_ANCHOR,
    RESET_MUTATION_REPLACEMENT,
    RESET_QUERY_ANCHOR,
    RESET_QUERY_REPLACEMENT,
    SELECTED_USAGE_ANCHOR,
    SELECTED_USAGE_REPLACEMENT,
    STOP_ROUTER_PROCESSES_SCRIPT,
    USAGE_STATUS_ANCHOR,
    USAGE_STATUS_REPLACEMENT,
    WINDOWS_RENDERER_PROFILES,
    default_destination,
    default_source,
    create_desktop_shortcut,
    legacy_router_profile_directory,
    next_backup_path,
    patch_windows_feature_bundles,
    patch_windows_login_bundles,
    source_from_store_package,
    stop_router_processes,
    router_profile_directory,
    validate_install_paths,
    validate_managed_destination,
)

ROOT = Path(__file__).resolve().parent.parent


class WindowsRendererPatchTests(unittest.TestCase):
    def make_renderer(self, root: Path, profile=None) -> Path:
        profile = profile or {
            "profile_query_anchor": PROFILE_QUERY_ANCHOR,
            "usage_status_anchor": USAGE_STATUS_ANCHOR,
            "plugin_request_anchor": PLUGIN_REQUEST_ANCHOR,
            "reset_query_anchor": RESET_QUERY_ANCHOR,
            "reset_mutation_anchor": RESET_MUTATION_ANCHOR,
            "selected_usage_anchor": SELECTED_USAGE_ANCHOR,
            "reset_header_anchor": RESET_HEADER_ANCHOR,
            "profile_picker_anchor": PROFILE_PICKER_ANCHOR,
            "plugin_picker_anchor": PLUGIN_PICKER_ANCHOR,
        }
        assets = root / "webview" / "assets"
        assets.mkdir(parents=True)
        (assets / "app-initial-fixture.js").write_text(
            "\n".join(
                (
                    profile["profile_query_anchor"],
                    profile["usage_status_anchor"],
                    profile["plugin_request_anchor"],
                    profile["reset_query_anchor"],
                    profile["reset_mutation_anchor"],
                    profile["selected_usage_anchor"],
                    profile["reset_header_anchor"],
                )
            ),
            encoding="utf-8",
        )
        (assets / "profile-fixture.js").write_text(
            profile["profile_picker_anchor"], encoding="utf-8"
        )
        (assets / "plugins-settings-fixture.js").write_text(
            profile["plugin_picker_anchor"], encoding="utf-8"
        )
        return assets

    def assert_replacements(self, assets: Path, profile=None) -> None:
        expected = profile or {
            "profile_query_replacement": PROFILE_QUERY_REPLACEMENT,
            "usage_status_replacement": USAGE_STATUS_REPLACEMENT,
            "plugin_request_replacement": PLUGIN_REQUEST_REPLACEMENT,
            "reset_query_replacement": RESET_QUERY_REPLACEMENT,
            "reset_mutation_replacement": RESET_MUTATION_REPLACEMENT,
            "selected_usage_replacement": SELECTED_USAGE_REPLACEMENT,
            "reset_header_replacement": RESET_HEADER_REPLACEMENT,
            "profile_picker_replacement": PROFILE_PICKER_REPLACEMENT,
            "plugin_picker_replacement": PLUGIN_PICKER_REPLACEMENT,
        }
        initial = next(assets.glob("app-initial-*.js")).read_text(encoding="utf-8")
        profile_source = next(assets.glob("profile-*.js")).read_text(encoding="utf-8")
        plugins = next(assets.glob("plugins-settings-*.js")).read_text(encoding="utf-8")
        for replacement in (
            expected["profile_query_replacement"],
            expected["usage_status_replacement"],
            expected["plugin_request_replacement"],
            expected["reset_query_replacement"],
            expected["reset_mutation_replacement"],
            expected["selected_usage_replacement"],
            expected["reset_header_replacement"],
        ):
            self.assertIn(replacement, initial)
        self.assertIn(expected["profile_picker_replacement"], profile_source)
        self.assertIn(expected["plugin_picker_replacement"], plugins)

    def make_login_bundles(self, root: Path) -> tuple[Path, Path]:
        build = root / ".vite" / "build"
        build.mkdir(parents=True)
        preload = build / "preload.js"
        preload.write_text(
            "let electron = require('electron');" + LOGIN_PRELOAD_TRAILER_ANCHOR,
            encoding="utf-8",
        )
        main = build / "main-fixture.js"
        main.write_text(
            LOGIN_MAIN_TRAILER_ANCHOR + "main-fixture.js.map",
            encoding="utf-8",
        )
        return preload, main

    def test_patches_all_scoped_windows_surfaces(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            assets = self.make_renderer(root)
            patch_windows_feature_bundles(root)
            self.assert_replacements(assets)

    def test_rejects_duplicate_profile_anchor(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            assets = self.make_renderer(root)
            (assets / "profile-duplicate.js").write_text(
                PROFILE_PICKER_ANCHOR, encoding="utf-8"
            )
            with self.assertRaisesRegex(RuntimeError, "Profile settings"):
                patch_windows_feature_bundles(root)

    def test_patches_private_login_preload_and_main_process(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            preload, main = self.make_login_bundles(root)
            patched_preload, patched_main = patch_windows_login_bundles(root)
            self.assertEqual(patched_preload, preload)
            self.assertEqual(patched_main, main)
            preload_source = preload.read_text(encoding="utf-8")
            main_source = main.read_text(encoding="utf-8")
            self.assertIn("codexMuxLoginWindow", preload_source)
            self.assertIn("codex-mux:open-isolated-login", preload_source)
            self.assertIn("codex-mux-login-${randomUUID()}", main_source)
            self.assertIn("sandbox: true", main_source)
            self.assertIn("nodeIntegration: false", main_source)
            self.assertIn("webSecurity: true", main_source)
            self.assertIn("setPermissionRequestHandler", main_source)
            self.assertIn("clearStorageData", main_source)
            self.assertIn("codexMuxUpdater", preload_source)
            self.assertIn("router-updater", main_source)
            self.assertNotIn("shell.openExternal", main_source)

    def test_patches_the_current_renderer_profile(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            assets = self.make_renderer(root, CURRENT_RENDERER_PROFILE)
            self.assertEqual(len(WINDOWS_RENDERER_PROFILES), 2)
            patch_windows_feature_bundles(root, CURRENT_RENDERER_PROFILE)
            self.assert_replacements(assets, CURRENT_RENDERER_PROFILE)

    def test_current_usage_reset_hook_uses_the_renderer_react_namespace(self) -> None:
        replacement = CURRENT_RENDERER_PROFILE["reset_query_replacement"]
        self.assertIn("rxa.useSyncExternalStore", replacement)
        self.assertNotIn("n$s.useSyncExternalStore", replacement)

    def test_rejects_duplicate_private_login_preload_anchor(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            preload, _main = self.make_login_bundles(root)
            preload.write_text(
                preload.read_text(encoding="utf-8") + LOGIN_PRELOAD_TRAILER_ANCHOR,
                encoding="utf-8",
            )
            with self.assertRaisesRegex(RuntimeError, "private login preload"):
                patch_windows_login_bundles(root)

    def test_menu_uses_the_private_login_bridge_without_window_open(self) -> None:
        menu = (ROOT / "ui" / "windows-router-menu.js").read_text(encoding="utf-8")
        self.assertIn("codexMuxLoginWindow", menu)
        self.assertIn("codexMuxUpdater", menu)
        self.assertIn("async function nativeUsageStatus", menu)
        self.assertIn('request("/usage")', menu)
        self.assertIn("Update now", menu)
        self.assertIn("fresh temporary browser session", menu)
        self.assertIn("await showBrowserLogin", menu)
        self.assertNotIn("window.open(", menu)

    def test_store_registration_is_the_first_source_choice(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "StorePackage" / "app"
            process_source = Path(directory) / "RunningProcess" / "app"
            with mock.patch(
                "patch_windows.source_from_store_package", return_value=source
            ) as package_discovery, mock.patch(
                "patch_windows.source_from_running_process", return_value=process_source
            ) as process_discovery, mock.patch(
                "patch_windows.source_from_windowsapps_glob", return_value=None
            ) as glob_discovery:
                self.assertEqual(default_source(), source)
            package_discovery.assert_called_once_with()
            process_discovery.assert_not_called()
            glob_discovery.assert_not_called()

    def test_store_registration_discovery_uses_get_appxpackage(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            install_location = Path(directory) / "OpenAI.Codex_fixture"
            app = install_location / "app"
            (app / "resources").mkdir(parents=True)
            (app / "ChatGPT.exe").touch()
            (app / "resources" / "app.asar").touch()
            completed = subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=str(install_location),
                stderr="",
            )
            with mock.patch(
                "patch_windows.subprocess.run", return_value=completed
            ) as run:
                self.assertEqual(source_from_store_package(), app)
            command = run.call_args.args[0]
            self.assertEqual(command[0].lower(), "powershell.exe")
            self.assertIn("Get-AppxPackage", command[-1])
            self.assertIn("$_.Architecture -eq 'X64'", command[-1])

    def test_destination_is_limited_to_the_managed_per_user_app_path(self) -> None:
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(
            os.environ, {"LOCALAPPDATA": directory}, clear=False
        ):
            expected = default_destination()
            self.assertEqual(validate_managed_destination(expected), expected.resolve())
            with self.assertRaisesRegex(RuntimeError, "managed per-user path"):
                validate_managed_destination(Path(directory) / "somewhere-else")
            with self.assertRaisesRegex(RuntimeError, "must not overlap"):
                validate_install_paths(expected, expected)

    def test_router_stop_targets_only_processes_below_the_router_install_root(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            destination = Path(directory) / "app"
            destination.mkdir()
            completed = subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout="",
                stderr="",
            )
            with mock.patch(
                "patch_windows.subprocess.run", return_value=completed
            ) as run:
                stop_router_processes(destination)
            command = run.call_args.args[0]
            environment = run.call_args.kwargs["env"]
            self.assertEqual(command[0].lower(), "powershell.exe")
            self.assertIn("Get-CimInstance Win32_Process", command[-1])
            self.assertIn("Stop-Process -Id", command[-1])
            self.assertNotIn("Stop-Process -Name", command[-1])
            self.assertEqual(
                environment["CODEX_MUX_ROUTER_INSTALL_ROOT"], str(destination.parent)
            )
            self.assertIn("OrdinalIgnoreCase", STOP_ROUTER_PROCESSES_SCRIPT)

    def test_shortcut_is_direct_and_uses_the_dedicated_profile(self) -> None:
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(
            os.environ,
            {
                "APPDATA": str(Path(directory) / "Roaming"),
            },
            clear=False,
        ):
            destination = Path(directory) / "app"
            destination.mkdir()
            (destination / "ChatGPT.exe").touch()
            shortcut_path = Path(directory) / "Desktop" / "Codex Relay.lnk"
            completed = subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=str(shortcut_path),
                stderr="",
            )
            with mock.patch(
                "patch_windows.subprocess.run", return_value=completed
            ) as run:
                self.assertEqual(create_desktop_shortcut(destination), shortcut_path)
            environment = run.call_args.kwargs["env"]
            self.assertEqual(
                environment["CODEX_MUX_SHORTCUT_TARGET"],
                str(destination / "ChatGPT.exe"),
            )
            self.assertEqual(
                environment["CODEX_MUX_SHORTCUT_WORKING_DIRECTORY"], str(destination)
            )
            self.assertEqual(
                environment["CODEX_MUX_SHORTCUT_PROFILE"],
                str(Path(directory) / "Roaming" / "Codex Relay"),
            )
            self.assertIn("$shortcut.TargetPath = $target", CREATE_SHORTCUT_SCRIPT)
            self.assertIn("--user-data-dir", CREATE_SHORTCUT_SCRIPT)

    def test_backup_path_is_unique_when_two_upgrades_begin_in_one_second(self) -> None:
        with tempfile.TemporaryDirectory() as directory, mock.patch(
            "patch_windows.time.strftime", return_value="20260818-120000"
        ):
            state_root = Path(directory) / "state"
            first_parent = state_root / "backups" / "20260818-120000"
            first_parent.mkdir(parents=True)
            expected = state_root / "backups" / "20260818-120000-1" / "app"
            self.assertEqual(next_backup_path(state_root), expected)

    def test_double_click_installer_calls_the_safe_patcher_path(self) -> None:
        cmd = (ROOT / "Install Codex Relay.cmd").read_text(encoding="utf-8")
        powershell = (ROOT / "scripts" / "install_windows.ps1").read_text(
            encoding="utf-8"
        )
        self.assertIn("install_windows.ps1", cmd)
        self.assertIn("patch_windows.py", powershell)
        self.assertIn("'--force', '--launch'", powershell)
        self.assertIn("Get-Command py.exe", powershell)
        self.assertIn("never targets", powershell)
        self.assertIn("sourceIsCheckout", powershell)
        self.assertIn("Older 0.3.1 updater helpers", powershell)

    def test_one_line_bootstrap_verifies_the_release_before_installing(self) -> None:
        bootstrap = (ROOT / "scripts" / "bootstrap_windows.ps1").read_text(
            encoding="utf-8"
        )
        self.assertIn("windows-update.json", bootstrap)
        self.assertIn("sourceSha256", bootstrap)
        self.assertIn("Get-FileHash", bootstrap)
        self.assertIn("Assert-SafeArchive", bootstrap)
        self.assertIn("scripts\\install_windows.ps1", bootstrap)
        self.assertIn("Codex Relay Bootstrap", bootstrap)
        self.assertIn("reserved for an explicit official Store app directory", bootstrap)
        self.assertNotIn("-File $installer -Source", bootstrap)
        self.assertNotIn("Start-Process", bootstrap)
        powershell = shutil.which("powershell.exe") or shutil.which("pwsh")
        if powershell is None:
            self.skipTest("PowerShell is not available on this host")
        bootstrap_path = str(ROOT / "scripts" / "bootstrap_windows.ps1").replace(
            "'", "''"
        )
        completed = subprocess.run(
            [
                powershell,
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                "[scriptblock]::Create((Get-Content -Raw -LiteralPath '"
                + bootstrap_path
                + "')) | Out-Null",
            ],
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        self.assertEqual(completed.returncode, 0, completed.stderr)

    def test_legacy_profile_is_preserved_during_the_brand_migration(self) -> None:
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(
            os.environ, {"APPDATA": str(Path(directory) / "Roaming")}, clear=False
        ):
            legacy = legacy_router_profile_directory()
            legacy.mkdir(parents=True)
            self.assertEqual(router_profile_directory(), legacy)

    @unittest.skipUnless(
        os.environ.get("CODEX_MUX_WINDOWS_RENDERER_DIR"),
        "set CODEX_MUX_WINDOWS_RENDERER_DIR to verify an unpacked Store renderer",
    )
    def test_current_renderer_when_supplied(self) -> None:
        source_assets = Path(os.environ["CODEX_MUX_WINDOWS_RENDERER_DIR"])
        self.assertTrue(source_assets.is_dir(), source_assets)
        with tempfile.TemporaryDirectory() as directory:
            assets = Path(directory) / "webview" / "assets"
            assets.mkdir(parents=True)
            for source in (
                next(source_assets.glob("app-initial-*.js")),
                next(source_assets.glob("profile-*.js")),
                next(source_assets.glob("plugins-settings-*.js")),
            ):
                shutil.copy2(source, assets / source.name)
            initial_source = next(assets.glob("app-initial-*.js")).read_text(encoding="utf-8")
            renderer_profile = (
                CURRENT_RENDERER_PROFILE
                if CURRENT_RENDERER_PROFILE["profile_query_anchor"] in initial_source
                else None
            )
            patch_windows_feature_bundles(Path(directory), renderer_profile)
            self.assert_replacements(assets, renderer_profile)


if __name__ == "__main__":
    unittest.main()
