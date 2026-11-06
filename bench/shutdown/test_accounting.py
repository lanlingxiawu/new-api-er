"""Checks that stop/restart reports reject accounting losses and replay."""

import copy
import unittest

from run import invariants


class ShutdownAccountingChecks(unittest.TestCase):
    def setUp(self):
        self.before = {
            "user_quota": 1000, "user_used": 0, "user_requests": 0,
            "token_remain": 1000, "token_used": 0, "channel_used": 0,
            "log_count": 0, "log_quota": 0, "log_distinct_ids": 0,
            "cost_count": 0, "cost_revenue": 0, "cost_quota": 0,
            "missing_costs": 0, "invalid_cost_links": 0,
            "duplicate_cost_links": 0, "consume_ids": [],
            "consume_records": [], "cost_records": [],
        }
        self.after = copy.deepcopy(self.before)
        self.after.update({
            "user_quota": 850, "user_used": 150, "user_requests": 1,
            "token_remain": 850, "token_used": 150, "channel_used": 150,
            "log_count": 1, "log_quota": 150, "log_distinct_ids": 1,
            "cost_count": 1, "cost_revenue": 150, "cost_quota": 150,
            "consume_ids": ["request-1"],
            "consume_records": [[1, "request-1", 150]],
            "cost_records": [[1, 1, 150, 150]],
        })
        self.load = {"requests": [{"outcome": "complete", "request_id": "request-1"}]}

    def test_complete_request_balances_and_records_match(self):
        self.assertTrue(invariants(self.before, self.after, self.load)["passed"])

    def test_missing_request_count_fails_even_when_money_matches(self):
        self.after["user_requests"] = 0
        result = invariants(self.before, self.after, self.load)
        self.assertFalse(result["request_count_matches"])
        self.assertFalse(result["passed"])

    def test_missing_or_duplicate_cost_link_cannot_pass_aggregate_checks(self):
        for field in ("missing_costs", "invalid_cost_links", "duplicate_cost_links"):
            with self.subTest(field=field):
                after = copy.deepcopy(self.after)
                after[field] = 1
                result = invariants(self.before, after, self.load)
                self.assertFalse(result["costs_match"])
                self.assertFalse(result["passed"])

    def test_restart_cannot_change_an_existing_record(self):
        before = copy.deepcopy(self.after)
        after = copy.deepcopy(before)
        after["consume_records"][0][2] = 999
        result = invariants(before, after, {"requests": []})
        self.assertFalse(result["old_records_unchanged"])
        self.assertFalse(result["passed"])

    def test_wallet_loss_cannot_be_hidden_by_successful_http(self):
        self.after["user_quota"] = 1000
        result = invariants(self.before, self.after, self.load)
        self.assertFalse(result["wallet_token_channel_match"])
        self.assertFalse(result["passed"])

    def test_accepted_request_requires_its_own_log_id(self):
        self.after["consume_ids"] = ["different-request"]
        self.after["consume_records"][0][1] = "different-request"
        self.assertFalse(invariants(self.before, self.after, self.load)["passed"])

    def test_abnormal_stop_does_not_relax_money_or_cost_consistency(self):
        self.after["token_used"] = 0
        self.assertFalse(invariants(self.before, self.after, self.load, strict=False)["passed"])

    def test_delivered_partial_response_requires_terminal_accounting(self):
        load = {"requests": [{
            "outcome": "interrupted", "request_id": "request-1",
            "stream": True, "status": 200, "received_content": True,
        }]}
        # Zero logs and zero charges are internally consistent, but this
        # accepted response delivered content and still needs final settlement.
        self.assertFalse(invariants(self.before, self.before, load, strict=False)["passed"])


if __name__ == "__main__":
    unittest.main()
