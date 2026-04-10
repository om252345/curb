const Module = require('module');
const path = require('path');

// Mock vscode
const vscodeMock = {
    workspace: {
        workspaceFolders: []
    },
    window: {
        showInformationMessage: () => Promise.resolve(),
        showErrorMessage: () => Promise.resolve()
    },
    EventEmitter: class {},
    ExtensionContext: class {},
    Uri: {
        file: (p) => ({ fsPath: p }),
        parse: (p) => ({ fsPath: p })
    }
};

const originalLoader = Module._load;
Module._load = function (request, parent, isMain) {
    if (request === 'vscode') {
        return vscodeMock;
    }
    return originalLoader.apply(this, arguments);
};

// Run mocha
const Mocha = require('mocha');
const mocha = new Mocha();

const fs = require('fs');
const testDir = 'out/test';

fs.readdirSync(testDir).filter(file => file.endsWith('.test.js')).forEach(file => {
    mocha.addFile(path.join(testDir, file));
});

mocha.run(failures => {
    process.exitCode = failures ? 1 : 0;
});
