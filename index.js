const db = require('./src/database');
const { connectToWhatsApp } = require('./src/whatsapp');

async function start() {
    console.log('Starting WhatsApp Monitor Bot...');
    
    // 1. Initialize DB and load cache
    await db.initDb();
    
    // 2. Start the Baileys socket
    await connectToWhatsApp();
}

start();