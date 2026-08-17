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
    default_destination,
    default_source,
    create_desktop_shortcut,
    next_backup_path,
    patch_windows_feature_bundles,
    source_from_store_package,
    stop_router_processes,
    validate_install_paths,
    validate_managed_destination,
)

ROOT = Path(__file__).resolve().parent.parent


class WindowsRendererPatchTests(unittest.TestCase):
    def make_renderer(self, root: Path) -> Path:
        assets = root / "webview" / "assets"
        assets.mkdir(parents=True)
        (assets / "app-initial-fixture.js").write_text(
            "\n".join(
                (
                    PROFILE_QUERY_ANCHOR,
                    PLUGIN_REQUEST_ANCHOR,
                    RESET_QUERY_ANCHOR,
                    RESET_MUTATION_ANCHOR,
                    SELECTED_USAGE_ANCHOR,
                    RESET_HEADER_ANCHOR,
                )
            ),
            encoding="utf-8",
        )
        (assets / "profile-fixture.js").write_text(
            PROFILE_PICKER_ANCHOR, encoding="utf-8"
        )
        (assets / "plugins-settings-fixture.js").write_text(
            PLUGIN_PICKER_ANCHOR, encoding="utf-8"
        )
        return assets

    def assert_replacements(self, assets: Path) -> None:
        initial = next(assets.glob("app-initial-*.js")).read_text(encoding="utf-8")
        profile = next(assets.glob("profile-*.js")).read_text(encoding="utf-8")
        plugins = next(assets.glob("plugins-settings-*.js")).read_text(encoding="utf-8")
        for replacement in (
            PROFILE_QUERY_REPLACEMENT,
            PLUGIN_REQUEST_REPLACEMENT,
            RESET_QUERY_REPLACEMENT,
            RESET_MUTATION_REPLACEMENT,
            SELECTED_USAGE_REPLACEMENT,
            RESET_HEADER_REPLACEMENT,
        ):
            self.assertIn(replacement, initial)
        self.assertIn(PROFILE_PICKER_REPLACEMENT, profile)
        self.assertIn(PLUGIN_PICKER_REPLACEMENT, plugins)

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
            shortcut_path = Path(directory) / "Desktop" / "Codex Subscription Router.lnk"
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
                str(Path(directory) / "Roaming" / "Codex Subscription Router"),
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
        cmd = (ROOT / "Install Codex Subscription Router.cmd").read_text(encoding="utf-8")
        powershell = (ROOT / "scripts" / "install_windows.ps1").read_text(
            encoding="utf-8"
        )
        self.assertIn("install_windows.ps1", cmd)
        self.assertIn("patch_windows.py", powershell)
        self.assertIn("'--force', '--launch'", powershell)
        self.assertIn("Get-Command py.exe", powershell)
        self.assertIn("never targets", powershell)

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
            patch_windows_feature_bundles(Path(directory))
            self.assert_replacements(assets)


if __name__ == "__main__":
    unittest.main()
