"""Hermes loads a directory plugin as hermes_plugins.<slug>, with nothing named
hermes_chat on the import path. Every module must load that way."""
import importlib
import importlib.util
import sys
from pathlib import Path

import pytest

PACKAGE = Path(__file__).resolve().parents[1] / "hermes_chat"
LOADED_AS = "hermes_plugins_under_test.hermes_chat"
MODULES = sorted(p.stem for p in PACKAGE.glob("*.py") if p.stem != "__init__")
NEEDS_HERMES = {"adapter", "probe"}


class _NoHermesChat:
    def find_spec(self, name, path=None, target=None):
        if name == "hermes_chat" or name.startswith("hermes_chat."):
            raise ModuleNotFoundError(f"No module named {name!r}", name=name)
        return None


@pytest.fixture
def loaded_as_plugin(monkeypatch):
    for name in [n for n in sys.modules if n == "hermes_chat" or n.startswith("hermes_chat.")]:
        monkeypatch.delitem(sys.modules, name)
    monkeypatch.setattr(sys, "meta_path", [_NoHermesChat(), *sys.meta_path])
    spec = importlib.util.spec_from_file_location(
        LOADED_AS, PACKAGE / "__init__.py", submodule_search_locations=[str(PACKAGE)])
    package = importlib.util.module_from_spec(spec)
    monkeypatch.setitem(sys.modules, LOADED_AS, package)
    spec.loader.exec_module(package)
    yield package
    for name in [n for n in sys.modules if n.startswith(LOADED_AS + ".")]:
        del sys.modules[name]


@pytest.mark.parametrize("module", MODULES)
def test_module_loads_under_the_plugin_loaders_name(loaded_as_plugin, module):
    if module in NEEDS_HERMES:
        pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")
    loaded = importlib.import_module(f"{LOADED_AS}.{module}")
    assert loaded.__name__ == f"{LOADED_AS}.{module}"
    assert not [n for n in sys.modules if n == "hermes_chat" or n.startswith("hermes_chat.")]
