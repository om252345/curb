import * as assert from 'assert';
import * as fs from 'fs';
import * as path from 'path';
import * as os from 'os';
import { rewriteMcpJsonEntry, restoreMcpJsonEntry } from '../mcpConfigStore';

describe('MCP Config Store Unit Tests', () => {
    const tmpDir = path.join(os.tmpdir(), 'curb-test-' + Math.random().toString(36).substring(7));
    const mcpPath = path.join(tmpDir, 'mcp.json');

    before(() => {
        if (!fs.existsSync(tmpDir)) {
            fs.mkdirSync(tmpDir, { recursive: true });
        }
    });

    after(() => {
        if (fs.existsSync(tmpDir)) {
            fs.rmSync(tmpDir, { recursive: true });
        }
    });

    it('should correctly rewrite an mcp.json entry', () => {
        const initialContent = {
            mcpServers: {
                "test-server": {
                    command: "uvx",
                    args: ["mcp-test"],
                    env: { TEST: "1" }
                }
            }
        };
        fs.writeFileSync(mcpPath, JSON.stringify(initialContent, null, 2));

        const curbBinaryPath = '/usr/local/bin/curb';
        const result = rewriteMcpJsonEntry(mcpPath, 'test-server', curbBinaryPath);

        assert.ok(result);
        assert.strictEqual(result!.originalUpstream, 'uvx mcp-test');
        assert.strictEqual(result!.originalEnv.TEST, '1');

        const reRead = JSON.parse(fs.readFileSync(mcpPath, 'utf8'));
        const entry = reRead.mcpServers['test-server'];
        assert.ok(entry.command.includes('curb'));
        assert.deepStrictEqual(entry.args, ['mcp-proxy', 'test-server']);
    });

    it('should correctly restore an mcp.json entry', () => {
        const originalUpstream = 'uvx mcp-test';
        const success = restoreMcpJsonEntry(mcpPath, 'test-server', originalUpstream);

        assert.strictEqual(success, true);
        const reRead = JSON.parse(fs.readFileSync(mcpPath, 'utf8'));
        const entry = reRead.mcpServers['test-server'];
        assert.strictEqual(entry.command, 'uvx');
        assert.deepStrictEqual(entry.args, ['mcp-test']);
    });

    it('should return null if server does not exist', () => {
        const result = rewriteMcpJsonEntry(mcpPath, 'non-existent', '/tmp/curb');
        assert.strictEqual(result, null);
    });
});
