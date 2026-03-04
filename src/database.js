const { open } = require('sqlite');
const sqlite3 = require('sqlite3');

let db;
const monitoredCache = new Set(); 

async function initDb() {
    db = await open({
        filename: './whatsapp_logs.db',
        driver: sqlite3.Database
    });

    await db.exec(`
        CREATE TABLE IF NOT EXISTS monitored_chats (
            chatId TEXT PRIMARY KEY
        );
        CREATE TABLE IF NOT EXISTS messages (
            id TEXT PRIMARY KEY,
            chatId TEXT,
            sender TEXT,
            content TEXT,
            timestamp INTEGER
        );
        -- The hidden cache to make !logmessages work on demand
        CREATE TABLE IF NOT EXISTS history_cache (
            id TEXT PRIMARY KEY,
            chatId TEXT,
            sender TEXT,
            content TEXT,
            timestamp INTEGER
        );
        -- Tracks all chats for the !groups and !chats commands
        CREATE TABLE IF NOT EXISTS known_chats (
            chatId TEXT PRIMARY KEY,
            name TEXT,
            isGroup INTEGER
        );
    `);

    const rows = await db.all('SELECT chatId FROM monitored_chats');
    rows.forEach(row => monitoredCache.add(row.chatId));
    
    console.log(`✅ Database ready. Loaded ${monitoredCache.size} monitored chats into memory.`);
}

function isChatMonitored(chatId) {
    return monitoredCache.has(chatId);
}

async function setChatMonitor(chatId, monitor) {
    if (monitor) {
        await db.run('INSERT OR IGNORE INTO monitored_chats (chatId) VALUES (?)', [chatId]);
        monitoredCache.add(chatId);
    } else {
        await db.run('DELETE FROM monitored_chats WHERE chatId = ?', [chatId]);
        monitoredCache.delete(chatId);
    }
}

async function getMonitoredChats() {
    return Array.from(monitoredCache);
}

// Saves to your permanent, official log
async function saveMessage(id, chatId, sender, content, timestamp) {
    return await db.run(
        'INSERT OR IGNORE INTO messages (id, chatId, sender, content, timestamp) VALUES (?, ?, ?, ?, ?)',
        [id, chatId, sender, content, timestamp]
    );
}

// Saves to the background cache
async function saveToCache(id, chatId, sender, content, timestamp) {
    return await db.run(
        'INSERT OR IGNORE INTO history_cache (id, chatId, sender, content, timestamp) VALUES (?, ?, ?, ?, ?)',
        [id, chatId, sender, content, timestamp]
    );
}

// Remembers group/chat names for your lists
async function updateKnownChat(chatId, name, isGroup) {
    if (name) {
        await db.run('INSERT OR REPLACE INTO known_chats (chatId, name, isGroup) VALUES (?, ?, ?)', [chatId, name, isGroup ? 1 : 0]);
    } else {
        await db.run('INSERT OR IGNORE INTO known_chats (chatId, isGroup) VALUES (?, ?)', [chatId, isGroup ? 1 : 0]);
    }
}

async function getKnownChats(isGroup) {
    return await db.all('SELECT * FROM known_chats WHERE isGroup = ?', [isGroup ? 1 : 0]);
}

// The core logic for !logmessages
async function transferFromCache(chatId, limit) {
    const limitNum = limit.toLowerCase() === 'all' ? 100000 : parseInt(limit);
    if (isNaN(limitNum)) return 0;
    
    // Pull the latest X messages from the hidden cache
    const rows = await db.all(
        'SELECT * FROM history_cache WHERE chatId = ? ORDER BY timestamp DESC LIMIT ?',
        [chatId, limitNum]
    );
    
    let saved = 0;
    for (const row of rows) {
        // Insert them into the permanent log
        const result = await db.run(
            'INSERT OR IGNORE INTO messages (id, chatId, sender, content, timestamp) VALUES (?, ?, ?, ?, ?)',
            [row.id, row.chatId, row.sender, row.content, row.timestamp]
        );
        if (result.changes > 0) saved++;
    }
    return saved;
}

module.exports = {
    initDb,
    isChatMonitored,
    setChatMonitor,
    getMonitoredChats,
    saveMessage,
    saveToCache,
    updateKnownChat,
    getKnownChats,
    transferFromCache
};