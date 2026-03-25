import ExcelJS from 'exceljs/dist/exceljs.bare.min.js'

self.onmessage = async (e) => {
  try {
    const wb = new ExcelJS.Workbook()
    await wb.xlsx.load(e.data)

    const sheets = wb.worksheets.map((ws) => {
      const colCount = ws.columnCount
      const headerRow = ws.getRow(1)

      const headers = []
      for (let i = 1; i <= colCount; i++) {
        const val = headerRow.getCell(i).value
        headers.push(val != null ? String(val) : `Col ${i}`)
      }

      // 2D array — much cheaper to clone than objects
      const rows = []
      ws.eachRow((row, rowNumber) => {
        if (rowNumber === 1) return
        const cells = []
        for (let i = 1; i <= colCount; i++) {
          const cell = row.getCell(i).value
          cells.push(cell != null ? String(cell) : '')
        }
        rows.push(cells)
      })

      return { name: ws.name, headers, rowCount: rows.length, rows }
    })

    // JSON string transfer is faster than structured clone for large flat data
    self.postMessage(JSON.stringify(sheets))
  } catch (err) {
    self.postMessage(JSON.stringify({ error: err.message }))
  }
}
