#!/usr/bin/env python3
"""Exercise the real Issue gate without checkout, credentials, or GitHub writes.

Uses only the standard library so this also runs in the existing PR changes job.
The small expression evaluator supports only the operators used by this workflow;
GitHub string equality is deliberately case-insensitive, unlike the JSON gate.
"""

import ast
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = (ROOT / '.github/workflows/sync-upstream.yml').read_text()


def job(name):
    match = re.search(rf'^  {name}:\n(.*?)(?=^  [\w-]+:|\Z)', WORKFLOW, re.M | re.S)
    assert match, f'missing job: {name}'
    return match.group(1)


def condition(block):
    match = re.search(r'^    if: >-\n((?:      .*\n)+)', block, re.M)
    assert match, 'missing folded job condition'
    return ' '.join(match.group(1).split())


GATE = job('authorize_issue')
SYNC = job('sync')
GATE_SCRIPT = textwrap.dedent(GATE.split('        run: |\n', 1)[1]).strip()
GATE_IF = condition(GATE)
SYNC_IF = condition(SYNC)
CHECKOUT_REF = re.search(r'^          ref: (.+)$', SYNC, re.M).group(1)


def expression(source, context, cancelled=False):
    """Evaluate the actual workflow condition's narrow, fail-closed syntax."""
    source = source.removeprefix('${{').removesuffix('}}').strip()
    source = source.replace('!cancelled()', str(not cancelled))

    def lookup(match):
        value = context
        for key in match.group().split('.'):
            value = value.get(key, {}) if isinstance(value, dict) else {}
        return repr(value if value != {} else None)

    source = re.sub(r'\b(?:github|needs)(?:\.\w+)+', lookup, source)
    tree = ast.parse(source.replace('&&', ' and ').replace('||', ' or '), mode='eval')

    def evaluate(node):
        if isinstance(node, ast.Constant):
            return node.value
        if isinstance(node, ast.Compare) and len(node.ops) == 1 and isinstance(node.ops[0], ast.Eq):
            left, right = evaluate(node.left), evaluate(node.comparators[0])
            if isinstance(left, str) and isinstance(right, str):
                return left.casefold() == right.casefold()
            return left == right
        if isinstance(node, ast.BoolOp):
            value = evaluate(node.values[0])
            for child in node.values[1:]:
                if isinstance(node.op, ast.And) and not value:
                    return value
                if isinstance(node.op, ast.Or) and value:
                    return value
                value = evaluate(child)
            return value
        raise AssertionError(f'unsupported workflow expression: {ast.dump(node)}')

    return evaluate(tree.body)


def event():
    return {
        'action': 'opened',
        'repository': {'full_name': 'hirsaeki/CLIProxyAPI', 'default_branch': 'main'},
        'issue': {'title': '[sync-upstream]', 'user': {'login': 'hirsaeki'}, 'body': ''},
        'sender': {'login': 'hirsaeki'},
    }


def context(payload=None, event_name='issues', actor='hirsaeki', triggering_actor='hirsaeki',
            repository='hirsaeki/CLIProxyAPI', result='success', allowed='true'):
    return {
        'github': {'event': event() if payload is None else payload, 'event_name': event_name,
                   'repository': repository, 'actor': actor, 'triggering_actor': triggering_actor},
        'needs': {'authorize_issue': {'result': result, 'outputs': {'allowed': allowed}}},
    }


class ForkSyncIssueTests(unittest.TestCase):
    def run_gate(self, ctx, raw=None):
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            payload = directory / 'event.json'
            output = directory / 'output'
            payload.write_text(json.dumps(ctx['github']['event']) if raw is None else raw)
            env = {
                'PATH': os.environ['PATH'], 'GITHUB_EVENT_PATH': str(payload),
                'GITHUB_OUTPUT': str(output),
                **{f'GITHUB_{key.upper()}': str(ctx['github'][key]) for key in
                   ('event_name', 'repository', 'actor', 'triggering_actor')},
            }
            result = subprocess.run(['bash', '-euo', 'pipefail', '-c', GATE_SCRIPT],
                                    env=env, cwd=directory, capture_output=True, text=True, timeout=5)
            value = output.read_text() if output.exists() else ''
            self.assertLessEqual({path.name for path in directory.iterdir()}, {'event.json', 'output'})
            self.assertTrue(value in ('', 'allowed=true\n', 'allowed=false\n'), value)
            return result.returncode == 0 and value == 'allowed=true\n'

    def test_owner_opened_issue_is_authorized(self):
        ctx = context()
        self.assertTrue(expression(GATE_IF, ctx))
        self.assertTrue(self.run_gate(ctx))
        self.assertTrue(expression(SYNC_IF, ctx))

    def test_exact_title_boundary(self):
        for title in ('[SYNC-UPSTREAM]', '[Sync-Upstream]', ' [sync-upstream]',
                      '[sync-upstream] ', '[sync-upstream]\n', '[sync-upstream]\r\n',
                      'prefix [sync-upstream]', '[sync-upstream] suffix', '', None,
                      '[sync-upstream]$(touch injected)', '${{ secrets.UPSTREAM_SYNC_TOKEN }}'):
            with self.subTest(title=title):
                payload = event()
                payload['issue']['title'] = title
                ctx = context(payload)
                self.assertFalse(self.run_gate(ctx))
        payload = event()
        payload['issue']['title'] = '[SYNC-UPSTREAM]'
        self.assertTrue(expression(GATE_IF, context(payload)), 'model GitHub case-insensitivity')

    def test_owner_sender_and_actor_boundaries(self):
        for user in ('other-user', 'hirsaeki-bot', 'Hirsaeki', '', None):
            for key in ('author', 'sender', 'actor', 'triggering_actor'):
                with self.subTest(user=user, key=key):
                    payload = event()
                    kwargs = {}
                    if key == 'author':
                        payload['issue']['user']['login'] = user
                    elif key == 'sender':
                        payload['sender']['login'] = user
                    else:
                        kwargs[key] = user
                    self.assertFalse(self.run_gate(context(payload, **kwargs)))

    def test_target_repository_boundary(self):
        for repository in ('other/CLIProxyAPI', 'hirsaeki/other', 'hirsaeki/cliproxyapi', ''):
            with self.subTest(repository=repository):
                self.assertFalse(self.run_gate(context(repository=repository)))
                payload = event()
                payload['repository']['full_name'] = repository
                self.assertFalse(self.run_gate(context(payload)))

    def test_only_new_issues_can_authorize(self):
        for action in ('edited', 'reopened', 'closed', 'labeled', 'created', '', None):
            payload = event()
            payload['action'] = action
            with self.subTest(action=action):
                self.assertFalse(self.run_gate(context(payload)))
        for name in ('issue_comment', 'pull_request', 'push', 'repository_dispatch',
                     'schedule', 'workflow_dispatch'):
            with self.subTest(event_name=name):
                self.assertFalse(self.run_gate(context(event_name=name)))
                self.assertFalse(expression(GATE_IF, context(event_name=name)))

    def test_missing_or_malformed_payload_fails_closed(self):
        for key in ('action', 'issue', 'sender', 'repository'):
            payload = event()
            del payload[key]
            with self.subTest(missing=key):
                self.assertFalse(self.run_gate(context(payload)))
        for raw in ('{', 'null', '[]', '{"issue": null}'):
            with self.subTest(raw=raw):
                self.assertFalse(self.run_gate(context(), raw=raw))

    def test_body_and_other_inputs_are_never_executed_or_selected(self):
        payload = event()
        payload['issue']['body'] = '\n'.join((
            '$(touch injected)', '`touch injected`', '${{ secrets.UPSTREAM_SYNC_TOKEN }}',
            'refs/heads/attacker', 'repo=attacker/evil', 'tag=v999.0.0', 'allowed=true',
            "PY\n__import__('os').system('exit 99')\nPY",
        ))
        payload['inputs'] = {'ref': 'attacker', 'repo': 'attacker/evil', 'tag': 'v999.0.0'}
        payload['issue']['labels'] = [{'name': 'sync-upstream'}]
        self.assertTrue(self.run_gate(context(payload)))
        self.assertEqual(expression(CHECKOUT_REF, context(payload)), 'refs/heads/main')
        for reference in ('github.event.issue', 'github.event.inputs', 'github.event.sender'):
            self.assertNotIn(reference, SYNC)
        self.assertNotIn("get('body'", GATE_SCRIPT)
        self.assertNotIn('${{', GATE_SCRIPT)

    def test_schedule_and_manual_survive_skipped_issue_gate(self):
        for name in ('schedule', 'workflow_dispatch'):
            ctx = context(payload={}, event_name=name, actor='other-user',
                          triggering_actor='other-user', result='skipped', allowed='')
            with self.subTest(event_name=name):
                self.assertFalse(expression(GATE_IF, ctx))
                self.assertTrue(expression(SYNC_IF, ctx))
                self.assertEqual(expression(CHECKOUT_REF, ctx), '')
                self.assertFalse(expression(SYNC_IF, ctx, cancelled=True))
        self.assertIn('!cancelled()', SYNC_IF, 'override implicit success() for skipped needs')

    def test_sync_job_rejects_gate_failure_and_other_events(self):
        for result in ('failure', 'skipped', 'cancelled', ''):
            with self.subTest(result=result):
                self.assertFalse(expression(SYNC_IF, context(result=result)))
        for allowed in ('false', '', None):
            with self.subTest(allowed=allowed):
                self.assertFalse(expression(SYNC_IF, context(allowed=allowed)))
        for name in ('issue_comment', 'pull_request', 'push', 'repository_dispatch'):
            self.assertFalse(expression(SYNC_IF, context(event_name=name)))
        self.assertFalse(expression(SYNC_IF, context(), cancelled=True))

    def test_rerun_cannot_reuse_gate_as_another_actor(self):
        for key in ('actor', 'triggering_actor'):
            ctx = context(**{key: 'other-user'})
            self.assertFalse(expression(SYNC_IF, ctx), 'reject even with cached gate success')
            self.assertFalse(self.run_gate(ctx))

    def test_rejected_issues_do_not_enter_sync_concurrency(self):
        self.assertNotRegex(WORKFLOW, r'(?m)^concurrency:')
        self.assertNotIn('concurrency:', GATE)
        self.assertIn('    needs: authorize_issue\n', SYNC)
        self.assertIn('    concurrency:\n      group: sync-upstream-${{ github.repository }}\n'
                      '      cancel-in-progress: false\n', SYNC)
        for allowed in ('', 'false'):
            self.assertFalse(expression(SYNC_IF, context(allowed=allowed)))
        self.assertEqual(WORKFLOW.count('group: sync-upstream-'), 1)

    def test_workflow_wiring_permissions_and_trusted_checkout(self):
        self.assertIn('on:\n  schedule:\n    - cron: "0 */4 * * *"\n'
                      '  workflow_dispatch:\n  issues:\n    types: [opened]\n', WORKFLOW)
        self.assertIn('permissions:\n  contents: read\n', WORKFLOW)
        self.assertEqual(WORKFLOW.count('permissions:'), 1)
        self.assertEqual(set(re.findall(r'^  ([\w-]+):', WORKFLOW.split('jobs:\n', 1)[1], re.M)),
                         {'authorize_issue', 'sync'})
        self.assertNotIn('secrets.', GATE)
        self.assertNotIn('uses:', GATE)
        self.assertIn('      allowed: ${{ steps.authorize.outputs.allowed }}', GATE)
        self.assertIn('        id: authorize', GATE)
        self.assertIn("ref: ${{ github.event_name == 'issues' && 'refs/heads/main' || '' }}", SYNC)
        self.assertIn('          token: ${{ secrets.UPSTREAM_SYNC_TOKEN }}', SYNC)
        self.assertIn('          fetch-depth: 0', SYNC)
        self.assertIn('  UPSTREAM: ""', WORKFLOW)
        self.assertIn('    timeout-minutes: 10', SYNC)
        ci = (ROOT / '.github/workflows/pr-test-build.yml').read_text()
        self.assertIn('run: python3 scripts/tests/fork-sync-issue_test.py', ci)


if __name__ == '__main__':
    unittest.main(verbosity=2)
