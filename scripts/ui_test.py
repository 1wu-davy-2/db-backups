import copy
import json
import os
import re
import unittest
from urllib.parse import parse_qs, urlparse

from playwright.sync_api import expect, sync_playwright

BASE = os.environ.get('UI_BASE', 'http://127.0.0.1:5173')
STAMP = '2026-11-17T08:00:00Z'
TARGET = dict(id=1, name='订单库', engine='postgres', host='127.0.0.1', port=5432,
              dbname='orders', username='backup_ro', ssl_mode='prefer', tags=['test'],
              server_version='17.6', server_version_num=170006, db_size_bytes=1024,
              is_enabled=True, last_probe_at=STAMP, created_at=STAMP)
JOB = dict(id=1, target_id=1, target_name='订单库', policy_id=None, trigger='manual',
           status='success', started_at=STAMP, finished_at=STAMP, size_bytes=1024,
           dump_sha256='abc123', tool_version='17.6', server_version='17.6',
           error_code='', error_message='', duration_ms=1000, created_at=STAMP)


class PageOperations(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.playwright = sync_playwright().start()
        cls.browser = cls.playwright.chromium.launch(
            channel=os.environ.get('UI_BROWSER_CHANNEL', 'chrome'), headless=True)
        expect.set_options(timeout=2500)

    @classmethod
    def tearDownClass(cls):
        cls.browser.close()
        cls.playwright.stop()

    def setUp(self):
        self.context = self.browser.new_context(viewport={'width': 1440, 'height': 1000})
        self.page = self.context.new_page()
        self.page.set_default_timeout(3500)
        self.errors = []
        self.page.on('pageerror', lambda error: self.errors.append(str(error)))
        self.requests = []
        self.role = 'admin'
        self.targets = [copy.deepcopy(TARGET)]
        self.policies = []
        self.jobs = [dict(JOB, id=i) for i in range(51, 0, -1)]
        self.tools = [dict(major=17, version='17.6', path='C:/PostgreSQL/17/bin/pg_dump.exe')]
        self.mysql_tools = []
        self.explore_bodies = []
        self.source_tables = [dict(schema='public', name='orders', kind='BASE TABLE')]
        self.query_result = dict(columns=['id', 'name'], rows=[['1', '<script>unsafe</script>'], ['2', None]], truncated=False, duration_ms=3)
        self.failures = {}
        self.hold = set()
        self.held = []
        self.deleted = False
        self.log_lines = ['[info] 磁盘历史日志', '[info] 备份完成']
        self.stream_lines = ['[info] 实时输出']
        self.context.route('**/api/**', self.api)

    def tearDown(self):
        for route in self.held:
            route.abort()
        self.context.unroute_all(behavior='ignoreErrors')
        self.context.close()
        self.assertEqual(self.errors, [], 'Browser runtime errors')

    def reply(self, route, data, status=200):
        route.fulfill(status=status, content_type='application/json', body=json.dumps(data))

    def api(self, route):
        request = route.request
        parsed = urlparse(request.url)
        path = parsed.path.removeprefix('/api')
        self.requests.append((request.method, path, parsed.query))
        key = (request.method, path)
        if key in self.hold:
            self.held.append(route)
            return
        if key in self.failures:
            return self.reply(route, {'error': self.failures[key]}, 400)
        if path == '/system/bootstrap':
            return self.reply(route, {'needs_setup': False, 'version': '0.1.0-m1'})
        if path == '/auth/me':
            return self.reply(route, dict(id=1, email='ui@example.com', role=self.role, created_at=STAMP))
        if path == '/stats':
            return self.reply(route, dict(targets=len(self.targets), policies=0, jobs_24h=51,
                                         failed_24h=0, live_bytes=1024, last_success_at=1794902400,
                                         running_jobs=0))
        if path == '/stats/daily':
            return self.reply(route, [dict(day='2026-11-17', total=51, success=51)])
        if path == '/system/info':
            return self.reply(route, dict(version='0.1.0-m1', toolchain=self.tools, storage='local',
                                         data_dir='./data', encryption='AES-256-GCM', mysql_toolchain=self.mysql_tools))
        if path == '/targets' and request.method == 'GET':
            return self.reply(route, self.targets)
        if path == '/targets' and request.method == 'POST':
            created = dict(TARGET, **request.post_data_json)
            self.targets.append(created)
            return self.reply(route, created, 201)
        if path == '/targets/1' and request.method == 'PUT':
            self.targets[0].update(request.post_data_json)
            return self.reply(route, self.targets[0])
        if path == '/targets/1' and request.method == 'DELETE':
            self.targets = []
            return self.reply(route, {'ok': True})
        if path == '/targets/1/test':
            return self.reply(route, dict(version='17.6', table_count=2, unreadable_tables=1, db_size_bytes=1024))
        if path == '/targets/1/backup':
            created = dict(JOB, id=52, status='running', finished_at=None)
            self.jobs.insert(0, created)
            return self.reply(route, created, 201)
        if path == '/targets/1/explore/cancel':
            return self.reply(route, {'ok': True})
        if path == '/targets/1/explore':
            body = request.post_data_json
            self.explore_bodies.append(body)
            if body['action'] == 'tables':
                return self.reply(route, self.source_tables)
            if body['action'] == 'structure':
                return self.reply(route, dict(columns=[dict(name='id', type='integer', nullable=False, default='', primary=True), dict(name='name', type='text', nullable=True, default='', primary=False)], indexes=[dict(name='orders_pkey', definition='PRIMARY KEY (id)')]))
            return self.reply(route, self.query_result)
        if path == '/policies':
            if request.method == 'POST':
                policy = dict(request.post_data_json, id=1, target_name='订单库', next_run_at=STAMP)
                self.policies.append(policy)
                return self.reply(route, policy, 201)
            return self.reply(route, self.policies)
        if path == '/jobs':
            query = parse_qs(parsed.query)
            items = self.jobs
            if query.get('status'):
                items = [j for j in items if j['status'] == query['status'][0]]
            if query.get('target_id'):
                items = [j for j in items if j['target_id'] == int(query['target_id'][0])]
            offset = int(query.get('offset', ['0'])[0])
            limit = int(query.get('limit', ['50'])[0])
            return self.reply(route, dict(items=items[offset:offset + limit], total=len(items)))
        if re.fullmatch(r'/jobs/\d+', path):
            return self.reply(route, next(j for j in self.jobs if j['id'] == int(path.split('/')[2])))
        if path.endswith('/logs/stream'):
            return route.fulfill(status=200, content_type='text/event-stream',
                                 body='event: reset\ndata: start\n\n' +
                                 ''.join(f'data: {line}\n\n' for line in self.stream_lines))
        if path.endswith('/logs'):
            return self.reply(route, {'lines': self.log_lines})
        if path.endswith('/artifacts'):
            if self.deleted:
                return self.reply(route, [])
            return self.reply(route, [dict(id=1, job_id=51, object_key='orders.dump', size_bytes=1024,
                                          sha256='abc123', encrypted=True, is_deleted=self.deleted,
                                          deleted_at=STAMP if self.deleted else None, created_at=STAMP)])
        if path.endswith('/cancel'):
            job = next(j for j in self.jobs if j['id'] == int(path.split('/')[2]))
            job['status'] = 'canceled'
            return self.reply(route, {'ok': True})
        if path == '/artifacts/1/download':
            return route.fulfill(status=200, body='test-backup', headers={
                'Content-Type': 'application/octet-stream',
                'Content-Disposition': 'attachment; filename="orders.dump"'})
        if path == '/audit':
            return self.reply(route, [])
        return self.reply(route, {'ok': True})

    def open(self, view=None):
        self.page.goto(BASE)
        self.page.wait_for_load_state('networkidle')
        if view:
            self.page.locator('.sidebar').get_by_role('button', name=view, exact=True).click()

    def fill_target(self):
        self.page.get_by_placeholder('订单库-生产').fill('测试库')
        self.page.get_by_placeholder('127.0.0.1').fill('127.0.0.1')
        self.page.get_by_placeholder('appdb').fill('orders')
        self.page.get_by_placeholder('backup_ro').fill('backup_ro')
        self.page.locator('input[type="password"]').fill('ui-test-password')

    def test_mysql_target_defaults(self):
        self.open('目标库')
        self.page.get_by_role('button', name='添加目标库', exact=True).click()
        self.page.get_by_label('数据库类型', exact=True).select_option('mysql')
        expect(self.page.get_by_label('端口', exact=True)).to_have_value('3306')
        expect(self.page.get_by_label('SSL 模式', exact=True)).to_have_value('verify-full')
        self.fill_target()
        self.page.get_by_role('button', name='连接测试并保存', exact=True).click()
        expect(self.page.get_by_role('dialog')).not_to_be_visible()
        self.assertEqual(self.targets[-1]['engine'], 'mysql')

    def test_mysql_policy_uses_sql_defaults(self):
        self.targets[0]['engine'] = 'mariadb'
        self.open('备份策略')
        self.page.get_by_role('button', name='新建策略', exact=True).click()
        expect(self.page.get_by_label('压缩', exact=True)).to_have_value('gzip:6')
        expect(self.page.get_by_label('备份后校验', exact=True)).to_have_value('checksum')
        self.page.get_by_role('button', name='保存策略', exact=True).click()
        expect(self.page.get_by_role('dialog')).not_to_be_visible()
        self.assertEqual(self.policies[0]['dump_format'], 'sql')

    def test_explorer_viewer_is_blocked(self):
        self.role = 'viewer'
        self.open('数据浏览')
        expect(self.page.get_by_text('数据浏览仅对管理员和操作员开放')).to_be_visible()

    def test_mysql_toolchain_diagnostics(self):
        self.mysql_tools = [dict(engine='mysql', major=8, minor=0, version='8.0.44', path='/tools/mysqldump')]
        self.open()
        expect(self.page.get_by_text('mysql 8.0.44', exact=True)).to_be_visible()
        expect(self.page.get_by_text('BACKUP_MYSQL_BIN_DIRS', exact=True)).to_be_visible()

    def open_table(self):
        self.open('数据浏览')
        self.page.get_by_role('button', name='连接并加载表', exact=True).click()
        self.page.locator('.explorer-table').first.click()
        expect(self.page.get_by_text('偏移 0 · 本页 2 行', exact=True)).to_be_visible()

    def test_explorer_tables_structure_and_safe_cells(self):
        self.open_table()
        expect(self.page.get_by_text('<script>unsafe</script>', exact=True)).to_be_visible()
        expect(self.page.get_by_text('NULL', exact=True)).to_be_visible()
        self.page.get_by_role('button', name='结构与索引', exact=True).click()
        expect(self.page.get_by_text('orders_pkey', exact=True)).to_be_visible()
        self.page.get_by_label('筛选表名', exact=True).fill('missing')
        expect(self.page.get_by_text('没有匹配的表', exact=True)).to_be_visible()
        self.assertEqual([b['action'] for b in self.explore_bodies], ['tables', 'structure', 'data'])

    def test_explorer_filter_and_variable_page_history(self):
        self.query_result['truncated'] = True
        self.open_table()
        self.page.get_by_role('button', name='下一页', exact=True).click()
        expect(self.page.get_by_text('偏移 2 · 本页 2 行', exact=True)).to_be_visible()
        self.page.get_by_role('button', name='下一页', exact=True).click()
        expect(self.page.get_by_text('偏移 4 · 本页 2 行', exact=True)).to_be_visible()
        self.page.get_by_role('button', name='上一页', exact=True).click()
        expect(self.page.get_by_text('偏移 2 · 本页 2 行', exact=True)).to_be_visible()
        self.page.get_by_label('筛选列', exact=True).select_option('name')
        self.page.get_by_label('筛选值', exact=True).fill("';drop table orders;--")
        self.page.get_by_role('button', name='应用筛选', exact=True).click()
        expect(self.page.get_by_text('偏移 0 · 本页 2 行', exact=True)).to_be_visible()
        self.assertEqual(self.explore_bodies[-1]['value'], "';drop table orders;--")

    def test_explorer_query_empty_error_and_cancel(self):
        self.query_result['rows'] = []
        self.open('数据浏览')
        self.page.get_by_role('button', name='SQL 查询', exact=True).click()
        self.page.get_by_label('只读 SQL', exact=True).fill('SELECT 1 WHERE 1=0')
        self.page.get_by_role('button', name='执行查询', exact=True).click()
        expect(self.page.get_by_text('查询成功，没有匹配的数据。', exact=True)).to_be_visible()
        self.failures[('POST', '/targets/1/explore')] = '仅支持单条 SELECT 查询'
        self.page.get_by_label('只读 SQL', exact=True).fill('DELETE FROM orders')
        self.page.get_by_role('button', name='执行查询', exact=True).click()
        expect(self.page.get_by_role('alert')).to_contain_text('仅支持单条 SELECT 查询')
        self.hold.add(('POST', '/targets/1/explore'))
        self.page.get_by_role('button', name='执行查询', exact=True).click()
        expect(self.page.get_by_label('目标库', exact=True)).to_be_disabled()
        self.page.get_by_role('button', name='取消查询', exact=True).click()
        expect(self.page.get_by_text('查询已取消', exact=True)).to_be_visible()
        self.assertTrue(any(path == '/targets/1/explore/cancel' for _, path, _ in self.requests))
        for route in self.held:
            route.abort()
        self.held.clear()

    def test_explorer_cancel_during_error_response_body(self):
        self.open('数据浏览')
        self.page.get_by_role('button', name='SQL 查询', exact=True).click()
        self.page.evaluate("""() => {
            const original = window.fetch;
            window.fetch = async (input, init) => {
                if (String(input).endsWith('/explore')) {
                    const response = new Response('', {status: 400});
                    response.json = () => new Promise((resolve, reject) => {
                        window.errorBodyStarted = true;
                        init.signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), {once: true});
                    });
                    return response;
                }
                return original(input, init);
            };
        }""")
        self.page.get_by_role('button', name='执行查询', exact=True).click()
        self.page.wait_for_function('window.errorBodyStarted === true')
        self.page.get_by_role('button', name='取消查询', exact=True).click()
        expect(self.page.get_by_text('查询已取消', exact=True)).to_be_visible()
        expect(self.page.get_by_role('alert')).not_to_be_visible()
        expect(self.page.get_by_role('button', name='执行查询', exact=True)).to_be_enabled()

    def test_explorer_mobile_long_values_stay_contained(self):
        self.page.set_viewport_size({'width': 390, 'height': 844})
        self.query_result['rows'][0][1] = 'x' * 1000
        self.open_table()
        self.assertLessEqual(self.page.evaluate('document.documentElement.scrollWidth'), 390)

    def test_new_target_password_is_required(self):
        self.open('目标库')
        self.page.get_by_role('button', name='添加目标库', exact=True).click()
        self.fill_target()
        self.page.locator('input[type="password"]').fill('')
        self.page.get_by_role('button', name='连接测试并保存', exact=True).click()
        expect(self.page.get_by_role('dialog')).to_be_visible()
        self.assertFalse(any(method == 'POST' and path == '/targets' for method, path, _ in self.requests))

    def test_target_error_is_inside_dialog(self):
        self.failures[('POST', '/targets')] = '连接失败：请检查地址'
        self.open('目标库')
        self.page.get_by_role('button', name='添加目标库', exact=True).click()
        self.fill_target()
        self.page.get_by_role('button', name='连接测试并保存', exact=True).click()
        expect(self.page.get_by_role('dialog').get_by_text('连接失败：请检查地址')).to_be_visible()

    def test_policy_error_is_inside_dialog(self):
        self.failures[('POST', '/policies')] = 'cron 表达式非法'
        self.open('备份策略')
        self.page.get_by_role('button', name='新建策略', exact=True).click()
        self.page.get_by_role('button', name='保存策略', exact=True).click()
        expect(self.page.get_by_role('dialog').get_by_text('cron 表达式非法')).to_be_visible()

    def test_target_form_cannot_close_during_save(self):
        self.hold.add(('POST', '/targets'))
        self.open('目标库')
        self.page.get_by_role('button', name='添加目标库', exact=True).click()
        self.fill_target()
        self.page.get_by_role('button', name='连接测试并保存', exact=True).click()
        dialog = self.page.get_by_role('dialog')
        expect(dialog.get_by_role('button', name='取消', exact=True)).to_be_disabled()
        self.page.keyboard.press('Escape')
        expect(dialog).to_be_visible()
        expect(dialog.locator('input').first).to_be_disabled()
        self.assertEqual(len(self.held), 1)
        self.reply(self.held.pop(), dict(TARGET, name='测试库'), 201)
        expect(dialog).not_to_be_visible()

    def test_row_action_is_disabled_while_testing(self):
        self.hold.add(('POST', '/targets/1/test'))
        self.open('目标库')
        self.page.get_by_role('button', name='测试', exact=True).click()
        expect(self.page.get_by_role('button', name='立即备份', exact=True)).to_be_disabled()
        self.assertEqual(len(self.held), 1)
        self.reply(self.held.pop(), dict(version='17.6', table_count=2, unreadable_tables=0, db_size_bytes=1024))
        expect(self.page.get_by_role('button', name='立即备份', exact=True)).to_be_enabled()

    def test_backup_opens_new_job(self):
        self.open('目标库')
        self.page.get_by_role('button', name='立即备份', exact=True).click()
        expect(self.page.get_by_role('dialog')).to_contain_text('任务 #52')
        expect(self.page.get_by_role('heading', name='备份任务', exact=True)).to_be_visible()

    def test_jobs_have_pagination_and_reset_filter(self):
        self.open('备份任务')
        expect(self.page.get_by_role('button', name='下一页', exact=True)).to_be_enabled()
        self.page.get_by_role('button', name='下一页', exact=True).click()
        expect(self.page.locator('tbody tr')).to_have_count(1)
        expect(self.page.locator('tbody')).to_contain_text('#1')
        self.page.get_by_label('任务状态').select_option('pending')
        expect(self.page.get_by_text('没有符合条件的任务', exact=True)).to_be_visible()
        expect(self.page.get_by_role('button', name='上一页', exact=True)).to_be_disabled()

    def test_finished_job_uses_history_not_stream(self):
        self.open('备份任务')
        self.page.locator('tbody tr').first.click()
        expect(self.page.get_by_role('dialog')).to_contain_text('磁盘历史日志')
        self.assertFalse(any(path.endswith('/logs/stream') for _, path, _ in self.requests))
        with self.page.expect_download() as download:
            self.page.get_by_role('link', name='下载', exact=True).click()
        self.assertEqual(download.value.suggested_filename, 'orders.dump')

    def test_log_error_can_be_retried(self):
        self.failures[('GET', '/jobs/51/logs')] = '日志读取失败'
        self.open('备份任务')
        self.page.locator('tbody tr').first.click()
        expect(self.page.get_by_role('dialog')).to_contain_text('日志读取失败')
        del self.failures[('GET', '/jobs/51/logs')]
        self.page.get_by_role('button', name='重试日志', exact=True).click()
        expect(self.page.get_by_role('dialog')).to_contain_text('磁盘历史日志')

    def test_deleted_artifact_has_no_download(self):
        self.deleted = True
        self.open('备份任务')
        self.page.locator('tbody tr').first.click()
        expect(self.page.get_by_role('dialog')).to_contain_text('已清理')
        expect(self.page.get_by_role('link', name='下载', exact=True)).to_have_count(0)

    def test_dashboard_shows_client_and_missing_client_hint(self):
        self.open()
        expect(self.page.get_by_text('pg_dump 17.6', exact=True)).to_be_visible()
        self.tools = []
        self.page.reload()
        expect(self.page.get_by_text('未发现可用的 pg_dump', exact=True)).to_be_visible()
        expect(self.page.get_by_text('backupd --check-tools', exact=True)).to_be_visible()

    def test_dashboard_job_opens_detail(self):
        self.open()
        self.page.get_by_role('button', name='查看任务 #51', exact=True).click()
        expect(self.page.get_by_role('dialog')).to_contain_text('任务 #51')

    def test_dialog_escape_restores_focus(self):
        self.open('目标库')
        trigger = self.page.get_by_role('button', name='添加目标库', exact=True)
        trigger.click()
        self.page.keyboard.press('Escape')
        expect(self.page.get_by_role('dialog')).not_to_be_visible()
        expect(trigger).to_be_focused()

    def test_disabled_target_cannot_start_backup(self):
        self.targets[0]['is_enabled'] = False
        self.open('目标库')
        expect(self.page.get_by_role('button', name='立即备份', exact=True)).to_be_disabled()

    def test_viewer_has_no_write_controls(self):
        self.role = 'viewer'
        self.jobs[0]['status'] = 'running'
        self.open('目标库')
        expect(self.page.get_by_role('button', name='立即备份', exact=True)).to_have_count(0)
        expect(self.page.get_by_role('button', name='添加目标库', exact=True)).to_have_count(0)
        self.page.locator('.sidebar').get_by_role('button', name='备份策略', exact=True).click()
        expect(self.page.get_by_role('button', name='新建策略', exact=True)).to_have_count(0)
        self.page.locator('.sidebar').get_by_role('button', name='备份任务', exact=True).click()
        self.page.locator('tbody tr').first.click()
        expect(self.page.get_by_role('button', name='取消任务', exact=True)).to_have_count(0)
        expect(self.page.get_by_role('link', name='下载', exact=True)).to_be_visible()

    def test_target_validation_and_edit_keep_password(self):
        self.open('目标库')
        self.page.get_by_role('button', name='添加目标库', exact=True).click()
        self.page.get_by_role('button', name='连接测试并保存', exact=True).click()
        self.assertFalse(any(method == 'POST' and path == '/targets' for method, path, _ in self.requests))
        self.page.keyboard.press('Escape')
        self.page.get_by_role('button', name='编辑', exact=True).click()
        self.page.get_by_label('名称', exact=True).fill('订单库-新名称')
        self.page.get_by_role('button', name='连接测试并保存', exact=True).click()
        expect(self.page.get_by_role('dialog')).not_to_be_visible()
        expect(self.page.locator('tbody')).to_contain_text('订单库-新名称')
        self.assertEqual(self.targets[0]['password'], '')

    def test_policy_presets_validation_and_save(self):
        self.open('备份策略')
        self.page.get_by_role('button', name='新建策略', exact=True).click()
        self.page.get_by_role('button', name='每 6 小时', exact=True).click()
        expect(self.page.get_by_label('执行时间（cron，5 段）')).to_have_value('0 */6 * * *')
        self.assertFalse(self.policies)
        self.page.get_by_label('保留份数', exact=True).fill('0')
        self.page.get_by_label('保留天数', exact=True).fill('0')
        self.page.get_by_role('button', name='保存策略', exact=True).click()
        expect(self.page.get_by_role('dialog')).to_contain_text('保留策略至少设置一项')
        self.page.get_by_label('保留份数', exact=True).fill('7')
        self.page.get_by_role('button', name='保存策略', exact=True).click()
        expect(self.page.get_by_role('dialog')).not_to_be_visible()
        expect(self.page.locator('tbody')).to_contain_text('0 */6 * * *')
        expect(self.page.get_by_role('button', name='新建策略', exact=True)).to_be_disabled()
        self.assertEqual(self.policies[0]['retain_count'], 7)

    def test_policy_save_locks_dialog(self):
        self.hold.add(('POST', '/policies'))
        self.open('备份策略')
        self.page.get_by_role('button', name='新建策略', exact=True).click()
        self.page.get_by_role('button', name='保存策略', exact=True).click()
        expect(self.page.get_by_role('dialog').get_by_role('button', name='取消', exact=True)).to_be_disabled()
        self.page.keyboard.press('Escape')
        expect(self.page.get_by_role('dialog')).to_be_visible()
        self.assertEqual(len(self.held), 1)
        self.reply(self.held.pop(), {'error': '保存失败，请重试'}, 500)
        expect(self.page.get_by_role('dialog')).to_contain_text('保存失败，请重试')
        expect(self.page.get_by_role('button', name='保存策略', exact=True)).to_be_enabled()

    def test_active_job_finishes_and_loads_history(self):
        self.jobs[0]['status'] = 'running'
        self.open('备份任务')
        self.page.locator('tbody tr').first.click()
        expect(self.page.get_by_role('dialog')).to_contain_text('实时输出')
        self.jobs[0]['status'] = 'success'
        expect(self.page.get_by_role('dialog')).to_contain_text('磁盘历史日志', timeout=7000)
        expect(self.page.get_by_role('dialog').get_by_role('status')).to_have_text('历史日志')
        expect(self.page.get_by_role('button', name='取消任务', exact=True)).to_have_count(0)

    def test_long_logs_follow_until_user_scrolls_up(self):
        self.jobs[0]['status'] = 'running'
        self.stream_lines = [f'[info] 实时输出 {i}' for i in range(120)]
        self.log_lines = self.stream_lines + ['[info] 备份完成']
        self.open('备份任务')
        self.page.locator('tbody tr').first.click()
        log = self.page.get_by_label('任务日志')
        follow = self.page.get_by_label('自动滚动')
        expect(log).to_contain_text('实时输出 119')
        expect(follow).to_be_checked()
        self.page.wait_for_function('''() => {
            const log = document.querySelector('.log');
            return log.scrollHeight - log.scrollTop - log.clientHeight < 2;
        }''')
        log.evaluate('(el) => { el.scrollTop = 0 }')
        expect(follow).not_to_be_checked()
        self.jobs[0]['status'] = 'success'
        expect(log).to_contain_text('备份完成', timeout=7000)
        expect(follow).not_to_be_checked()
        self.assertEqual(log.evaluate('(el) => el.scrollTop'), 0)
        follow.check()
        self.page.wait_for_function('''() => {
            const log = document.querySelector('.log');
            return log.scrollHeight - log.scrollTop - log.clientHeight < 2;
        }''')

    def test_log_follow_survives_artifact_appearing(self):
        self.jobs[0]['status'] = 'running'
        self.deleted = True
        self.stream_lines = [f'[info] 实时输出 {i}' for i in range(120)]
        self.log_lines = self.stream_lines + ['[info] 备份完成']
        self.open('备份任务')
        self.page.locator('tbody tr').first.click()
        expect(self.page.get_by_label('任务日志')).to_contain_text('实时输出 119')
        expect(self.page.get_by_label('自动滚动')).to_be_checked()
        self.jobs[0]['status'] = 'success'
        self.deleted = False
        expect(self.page.get_by_role('link', name='下载', exact=True)).to_be_visible(timeout=7000)
        expect(self.page.get_by_label('任务日志')).to_contain_text('备份完成')
        expect(self.page.get_by_label('自动滚动')).to_be_checked()

    def test_cancel_requires_confirmation_and_cannot_repeat(self):
        self.jobs[0]['status'] = 'running'
        self.hold.add(('POST', '/jobs/51/cancel'))
        self.open('备份任务')
        self.page.locator('tbody tr').first.click()
        self.page.once('dialog', lambda dialog: dialog.dismiss())
        self.page.get_by_role('button', name='取消任务', exact=True).click()
        self.assertFalse(self.held)
        self.page.once('dialog', lambda dialog: dialog.accept())
        self.page.get_by_role('button', name='取消任务', exact=True).click()
        expect(self.page.get_by_role('button', name='取消中…', exact=True)).to_be_disabled()
        self.assertEqual(len(self.held), 1)
        self.reply(self.held.pop(), {'ok': True})
        expect(self.page.get_by_role('button', name='已请求取消', exact=True)).to_be_disabled()

    def test_failed_target_list_can_retry(self):
        self.failures[('GET', '/targets')] = '目标库读取失败'
        self.open('目标库')
        expect(self.page.get_by_role('alert')).to_have_text('目标库读取失败')
        del self.failures[('GET', '/targets')]
        self.page.get_by_role('button', name='刷新', exact=True).click()
        expect(self.page.locator('tbody')).to_contain_text('订单库')
        expect(self.page.get_by_role('alert')).to_have_count(0)

    def test_system_error_does_not_hide_metrics(self):
        self.failures[('GET', '/system/info')] = '客户端状态读取失败'
        self.open()
        expect(self.page.get_by_role('region', name='备份客户端状态')).to_contain_text('客户端状态读取失败')
        expect(self.page.get_by_text('近 24 小时任务', exact=True)).to_be_visible()
        del self.failures[('GET', '/system/info')]
        self.page.get_by_role('button', name='重试', exact=True).click()
        expect(self.page.get_by_text('pg_dump 17.6', exact=True)).to_be_visible()

    def test_mobile_has_no_page_overflow(self):
        self.page.set_viewport_size({'width': 390, 'height': 844})
        self.open('备份任务')
        self.assertTrue(self.page.evaluate('document.documentElement.scrollWidth <= window.innerWidth'))


if __name__ == '__main__':
    unittest.main(verbosity=2)
