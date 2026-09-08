import asyncio
import unittest
from unittest.mock import patch, AsyncMock
from account import AccountCache, normalize


class QuotaTests(unittest.TestCase):
    def test_windows_follow_duration_not_slot(self):
        out = normalize({'rateLimits': {'primary': {'usedPercent': 87, 'windowDurationMins': 10080}, 'secondary': {'usedPercent': 24, 'windowDurationMins': 300}}})
        self.assertEqual(out['weekly']['remaining'], 13)
        self.assertEqual(out['five_hour']['remaining'], 76)

    def test_missing_window_is_not_zero_usage(self):
        out = normalize({'rateLimits': {'primary': {'usedPercent': 87, 'windowDurationMins': 10080}}})
        self.assertIsNone(out['five_hour'])

    def test_codex_bucket_selected(self):
        out = normalize({'rateLimitsByLimitId': {'codex': {'primary': {'usedPercent': 10, 'windowDurationMins': 300}}}, 'rateLimits': {'primary': {'usedPercent': 99, 'windowDurationMins': 300}}})
        self.assertEqual(out['five_hour']['remaining'], 90)


class CacheTests(unittest.IsolatedAsyncioTestCase):
    async def test_same_account_shares_query_and_epoch_clears_data(self):
        cache = AccountCache()
        with patch('account.fetch', new_callable=AsyncMock, return_value={'weekly': {'remaining': 13}}) as fetch:
            cache.snapshot('/test', '/codex', ('a',))
            cache.snapshot('/test', '/codex', ('a',))
            await asyncio.sleep(0)
            self.assertEqual(cache.snapshot('/test', '/codex', ('a',))['weekly']['remaining'], 13)
            self.assertEqual(fetch.await_count, 1)
            self.assertEqual(cache.snapshot('/test', '/codex', ('b',)), {})
            await cache.close()
