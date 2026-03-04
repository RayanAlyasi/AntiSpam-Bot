const db = require('./database');

function getMessageText(message) {
    if (!message) return '';
    return message.conversation || 
           (message.extendedTextMessage && message.extendedTextMessage.text) || 
           '';
}

async function handleHistorySync(sock, data) {
    const messages = data.messages || [];
    const chats = data.chats || [];
    
    console.log(`📥 Processing native sync: ${messages.length} messages, ${chats.length} chats...`);
    
    // Save chats so we know their IDs/names for !groups and !chats commands
    for (const chat of chats) {
        const isGroup = chat.id.endsWith('@g.us');
        await db.updateKnownChat(chat.id, chat.name || chat.id, isGroup);
    }
    
    let cachedCount = 0;
    for (const msg of messages) {
        if (!msg.message) continue;
        
        const remoteJid = msg.key.remoteJid;
        const sender = msg.key.participant || msg.key.remoteJid;
        const text = getMessageText(msg.message);
        
        if (remoteJid && text) {
            // 1. Quietly save ALL history to the background cache
            await db.saveToCache(msg.key.id, remoteJid, sender, text, msg.messageTimestamp);
            cachedCount++;
            
            // 2. If it's already a monitored chat, save to the permanent log too
            if (db.isChatMonitored(remoteJid)) {
                await db.saveMessage(msg.key.id, remoteJid, sender, text, msg.messageTimestamp);
            }
        }
    }
    console.log(`✅ Cached ${cachedCount} old messages into the background database.`);
}

async function handleMessage(sock, m) {
    const msg = m.messages[0];
    if (!msg.message || msg.key.remoteJid === 'status@broadcast') return;

    const remoteJid = msg.key.remoteJid;
    const isGroup = remoteJid.endsWith('@g.us');
    const sender = msg.key.participant || msg.key.remoteJid;
    const text = getMessageText(msg.message);

    // Update our known lists on every new message
    await db.updateKnownChat(remoteJid, msg.pushName || remoteJid, isGroup);

    if (text) {
        // Cache incoming messages to the background table so !logmessages works on recent unmonitored chats
        await db.saveToCache(msg.key.id, remoteJid, sender, text, msg.messageTimestamp);

        // Standard monitoring
        if (db.isChatMonitored(remoteJid)) {
            await db.saveMessage(msg.key.id, remoteJid, sender, text, msg.messageTimestamp);
        }
    }

    if (!msg.key.fromMe) return;

    const args = text.trim().split(' ');
    const command = args[0].toLowerCase();

    switch (command) {
        case '!monitor':
        case '!monitorgroup': {
            const targetJid = args[1] || remoteJid; 
            await db.setChatMonitor(targetJid, true);
            await sock.sendMessage(remoteJid, { text: `✅ Chat ${targetJid} is now being monitored.` });
            break;
        }

        case '!unmonitor':
        case '!unmonitorgroup': {
            const targetJid = args[1] || remoteJid;
            await db.setChatMonitor(targetJid, false);
            await sock.sendMessage(remoteJid, { text: `⛔ Chat ${targetJid} is no longer being monitored.` });
            break;
        }

        case '!groups': {
            const knownGroups = await db.getKnownChats(true);
            const monitoredList = await db.getMonitoredChats();
            const monitoredSet = new Set(monitoredList);

            let response = '*📋 Your Groups:*\n\n';
            for (const group of knownGroups) {
                const status = monitoredSet.has(group.chatId) ? '🟢 Monitored' : '⚪ Unmonitored';
                response += `*Name:* ${group.name}\n*ID:* ${group.chatId}\n*Status:* ${status}\n\n`;
            }
            if (knownGroups.length === 0) response += 'No groups found yet.';
            
            await sock.sendMessage(remoteJid, { text: response });
            break;
        }

        case '!chats': {
            const knownChats = await db.getKnownChats(false);
            const monitoredList = await db.getMonitoredChats();
            const monitoredSet = new Set(monitoredList);

            let response = '*👤 Your Private Chats:*\n\n';
            let count = 0;
            for (const chat of knownChats) {
                const status = monitoredSet.has(chat.chatId) ? '🟢 Monitored' : '⚪ Unmonitored';
                response += `*ID:* ${chat.chatId}\n*Status:* ${status}\n\n`;
                count++;
                
                // Prevent WhatsApp from rejecting the message if you have hundreds of chats
                if (count >= 30) {
                     response += `...and more.\n`;
                     break; 
                }
            }
            if (count === 0) response += 'No private chats found yet.';
            
            await sock.sendMessage(remoteJid, { text: response });
            break;
        }

        case '!logmessages': {
            const countParam = args[1];
            const targetChat = args[2] || remoteJid;

            if (!countParam) {
                await sock.sendMessage(remoteJid, { text: '❌ Usage: !logmessages <number|all> [chatId]' });
                return;
            }

            await sock.sendMessage(remoteJid, { text: `⏳ Moving messages for ${targetChat} from background cache to permanent log...` });

            // Pull from the background cache!
            const savedCount = await db.transferFromCache(targetChat, countParam);

            if (savedCount > 0) {
                await sock.sendMessage(remoteJid, { text: `✅ Successfully moved ${savedCount} historical messages into the active database.` });
            } else {
                await sock.sendMessage(remoteJid, { text: `⚠️ No old messages found in cache. The bot can only log messages that WhatsApp synced to it when it started.` });
            }
            break;
        }
    }
}

module.exports = { handleMessage, handleHistorySync };