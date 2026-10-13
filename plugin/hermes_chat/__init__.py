"""Hermes Chat platform plugin for Hermes.

The adapter is imported lazily so that importing the package never pulls in
Hermes modules.
"""


def register(ctx):
    from .adapter import register as _register

    _register(ctx)