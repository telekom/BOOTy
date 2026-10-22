// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0

const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const test = require('node:test');

test('real Renovate extracts live tool, image, full Debian version and source digest pins', async () => {
  const directory = path.resolve(`.renovate-extraction-${process.pid}`);
  fs.mkdirSync(directory);
  try {
    const env = {
      ...process.env,
      RENOVATE_BASE_DIR: directory,
      RENOVATE_CACHE_DIR: path.join(directory, 'cache'),
      TMPDIR: directory,
      RENOVATE_ONBOARDING: 'false',
      RENOVATE_REQUIRE_CONFIG: 'ignored',
      LOG_LEVEL: 'debug',
      LOG_FORMAT: 'json',
    };
    // Repository configuration must be loaded once, not also as global config.
    delete env.RENOVATE_CONFIG_FILE;
    const output = execFileSync('renovate', ['--platform=local', '--dry-run=extract'], {
      env, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024,
    });

    await test('real Renovate replaces pins and preserves native updater ownership', async () => {
      const binary = process.env.PATH.split(path.delimiter)
        .map(directory => path.join(directory, 'renovate')).find(file => fs.existsSync(file));
      assert.ok(binary, 'Run with the pinned Renovate CLI on PATH');
      const root = path.dirname(fs.realpathSync(binary));
      const load = file => import(pathToFileURL(path.join(root, file)).href);
      const { init } = await load('logger/index.js');
      await init();
      const { GlobalConfig } = await load('config/global.js');
      const { extractPackageFile } = await load('modules/manager/custom/regex/index.js');
      const { doAutoReplace } = await load('workers/repository/update/branch/auto-replace.js');
      const { applyPackageRules } = await load('util/package-rules/index.js');
      const config = JSON.parse(fs.readFileSync('.github/renovate.json'));
      const directory = path.resolve(`.renovate-mutation-${process.pid}`);
      fs.mkdirSync(directory);
      GlobalConfig.set({ localDir: directory, cacheDir: directory, platform: 'local' });
      try {
        for (const [depType, packageName, datasource, enabled] of [
          ['require', 'github.com/spf13/cobra', 'go', false],
          ['indirect', 'google.golang.org/grpc', 'go', false],
          ['golang', 'go', 'golang-version', true],
        ]) {
          const resolved = await applyPackageRules({
            packageRules: config.packageRules, manager: 'gomod', depType,
            packageName, depName: packageName, datasource,
          }, 'dependency ownership');
          assert.equal(resolved.enabled, enabled, packageName);
          if (depType === 'golang') {
            assert.equal(resolved.rangeStrategy, 'bump');
            assert.equal(resolved.allowedVersions, '<1.28.0');
          }
        }
        const fixtures = [
          [1, '# renovate: datasource=docker depName=nginx\nimage: nginx:alpine',
            'alpine', `sha256:${'b'.repeat(64)}`, 'nginx:alpine@sha256:'],
          [1, `# renovate: datasource=docker depName=alpine\nimage: alpine:3.20@sha256:${'a'.repeat(64)}`,
            '3.24', `sha256:${'b'.repeat(64)}`, 'image: alpine:3.24@sha256:'],
          [2, '# renovate: datasource=github-tags depName=lvmteam/lvm2\nARG LVM2_VERSION=2.03.27',
            '2_03_43', undefined, 'ARG LVM2_VERSION=2.03.43'],
          [3, '# renovate: datasource=deb depName=shim-signed\nARG SHIM_SIGNED_VERSION=1.51~1+deb13u1+16.1-2~deb13u1',
            '1.52~1+deb13u2+16.2-1~deb13u2', undefined, 'ARG SHIM_SIGNED_VERSION=1.52~1+deb13u2+16.2-1~deb13u2'],
          [4, `# renovate: datasource=git-refs depName=util-linux/util-linux\nARG UTIL_LINUX_REF=${'a'.repeat(40)}`,
            undefined, 'b'.repeat(40), `ARG UTIL_LINUX_REF=${'b'.repeat(40)}`],
        ];
        for (const [index, content, newValue, newDigest, expected] of fixtures) {
          const manager = { ...config.customManagers[index], matchStringsStrategy: 'any' };
          const extraction = extractPackageFile(content, 'fixture', manager);
          assert.ok(extraction);
          const result = await doAutoReplace({
            ...manager, ...extraction, ...extraction.deps[0],
            manager: 'regex', depIndex: 0, packageFile: 'fixture',
            newValue, newDigest, isPinDigest: index === 1,
          }, content, false);
          assert.ok(result.includes(expected), result);
          if (newDigest) assert.ok(result.includes(newDigest), result);
          assert.equal(fs.readFileSync(path.join(directory, 'fixture'), 'utf8'), result);
        }
        const manager = { ...config.customManagers[0], matchStringsStrategy: 'any' };
        const annotation = '# renovate: datasource=golang-version depName=go\n';
        let content = `${annotation}GO_VERSION=1.27.2\n${annotation}go-version: "1.27.2"\nUNRELATED=1.27.2`;
        const original = content;
        for (const depIndex of [0, 1]) {
          const extraction = extractPackageFile(content, 'fixture', manager);
          content = await doAutoReplace({
            ...manager, ...extraction, ...extraction.deps[depIndex],
            manager: 'regex', depIndex, packageFile: 'fixture', newValue: '1.27.3',
          }, content, false);
        }
        assert.equal(content, original.replace('GO_VERSION=1.27.2', 'GO_VERSION=1.27.3')
          .replace('go-version: "1.27.2"', 'go-version: "1.27.3"'));
      } finally {
        fs.rmSync(directory, { recursive: true });
      }
    });
    const record = output.trim().split('\n').map(line => JSON.parse(line))
      .find(entry => entry.msg === 'Extracted dependencies');
    assert.ok(record, 'Renovate must emit an extraction result');
    const files = record.packageFiles.regex;
    const deps = files.flatMap(file => file.deps);
    const expected = [
      ['srl-labs/containerlab', 2],
      ['golangci/golangci-lint', 2],
      ['orhun/git-cliff', 2],
      ['oras-project/oras', 2],
      ['golang.org/x/vuln', 1],
      ['go', 25],
      ['renovate', 1],
      ['quay.io/frrouting/frr', 24],
      ['golang', 1],
      ['alpine', 8],
      ['nginx', 14],
      ['ghcr.io/srl-labs/network-multitool', 5],
      ['lvmteam/lvm2', 1],
      ['shim-signed', 1],
      ['shim-helpers-amd64-signed', 1],
      ['grub-efi-amd64-signed', 1],
      ['https://github.com/util-linux/util-linux', 1],
    ];
    for (const [name, count] of expected) {
      const matches = deps.filter(dep => dep.depName === name);
      assert.equal(matches.length, count, name);
      assert.ok(matches.every(dep => typeof dep.currentValue === 'string' && dep.currentValue.length > 0));
    }
    assert.equal(deps.length, expected.reduce((total, row) => total + row[1], 0));
    assert.match(deps.find(dep => dep.datasource === 'git-refs').currentDigest, /^[a-f0-9]{40}$/);
    assert.ok(deps.filter(dep => dep.datasource === 'deb').every(dep => dep.currentValue.includes('+')),
      'Capture full Debian revisions, not their leading semantic version');
    assert.ok(record.packageFiles.gomod.flatMap(file => file.deps)
      .some(dep => dep.depName === 'go' && dep.depType === 'golang' && /^1\.27\.\d+$/.test(dep.currentValue)));
    assert.ok(files.every(file => file.packageFile !== 'go.mod' &&
      !(file.packageFile.endsWith('Dockerfile') && file.deps.some(dep => dep.datasource === 'docker'))),
    'Do not duplicate native Dependabot manifests');
  } finally {
    fs.rmSync(directory, { recursive: true });
  }
});
