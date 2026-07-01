import React from 'react';
import ReactDOM from 'react-dom/client';
import { configureAmplify } from './services/auth';
import './responsive.css';
import App from './App';

configureAmplify();

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
