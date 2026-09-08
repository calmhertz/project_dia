"""Desired-state synchronization with the Server.

Latest-state reconciliation (spec.md section 6.2): the Worker reports the
generation it holds and the Server either confirms it or sends the whole
current set. A failed sync is not an error condition for the Worker; it simply
keeps executing what it already has.
"""

import logging
from datetime import datetime, timezone

import grpc

from aagasa_worker.planstore import PlanStore

logger = logging.getLogger(__name__)

# Highest PassPlan schema version this Worker understands. A newer plan is
# refused rather than executed partially (worker-spec section 8).
SUPPORTED_PLAN_VERSION = 1


class DesiredStateSync:
    """Pulls the Worker's desired execution state and stores it."""

    def __init__(self, client, store: PlanStore):
        self._client = client
        self._store = store

    def sync_once(self) -> dict | None:
        """Fetch and apply the desired state.

        Returns a reconciliation summary, None when already current, and
        raises grpc.RpcError when the Server is unreachable so the caller can
        back off.
        """
        response = self._client.sync_pass_plans(self._store.generation)
        if not response.changed:
            logger.debug("desired state unchanged")
            return None

        plans = []
        for plan in response.plans.plans:
            # A plan this Worker cannot fully understand is skipped rather
            # than stored: executing part of it would be worse than not
            # running it at all.
            if not 1 <= plan.plan_version <= SUPPORTED_PLAN_VERSION:
                logger.error(
                    "skipping pass %s: plan version %d, this worker supports up to %d",
                    plan.pass_id, plan.plan_version, SUPPORTED_PLAN_VERSION)
                continue
            plans.append({
                "pass_id": plan.pass_id,
                "generation": plan.generation,
                "plan_version": plan.plan_version,
                "encoded": plan.SerializeToString(deterministic=True),
                "aos": _to_datetime(plan.aos),
                "los": _to_datetime(plan.los),
            })

        summary = self._store.replace_desired_state(plans, response.plans.generation)
        logger.info("synchronized %d plan(s) generation=%s",
                    len(plans), response.plans.generation)
        return summary


def _to_datetime(timestamp) -> datetime:
    return timestamp.ToDatetime(tzinfo=timezone.utc)
