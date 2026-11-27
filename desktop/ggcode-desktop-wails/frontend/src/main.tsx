import React from 'react'
import {createRoot} from 'react-dom/client'
import './assets/fonts/fonts.css'
import './style.css'
import { App } from './App'

const container = document.getElementById('root')

const root = createRoot(container!)

root.render(
    <React.StrictMode>
        <App/>
    </React.StrictMode>
)
