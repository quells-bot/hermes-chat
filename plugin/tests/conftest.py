import pytest

from stubserver import StubServer


@pytest.fixture
async def stub():
    server = StubServer()
    await server.start()
    yield server
    await server.stop()